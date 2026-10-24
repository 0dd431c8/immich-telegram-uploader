package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0dd431c8/immich-telegram-upload/config"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func testManager(ctx context.Context) *Manager {
	return New(config.Config{TelegramAdminUserID: 1234567890}, ctx, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testBot(t *testing.T, handler http.HandlerFunc) *bot.Bot {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	b, err := bot.New("test-token", bot.WithSkipGetMe(), bot.WithServerURL(server.URL), bot.WithHTTPClient(time.Second, server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileServer(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/bottest-token/getFile":
		_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"file-1","file_path":"media/photo.jpg"}}`)
	case "/file/bottest-token/media/photo.jpg":
		_, _ = io.WriteString(w, "media content")
	default:
		http.NotFound(w, r)
	}
}

func TestUploadsDisabledHandler(t *testing.T) {
	manager := testManager(context.Background())
	for _, tc := range []struct {
		id      int64
		disable bool
		want    bool
	}{
		{9876543210, true, false}, {1234567890, true, true},
		{9876543210, false, true}, {1234567890, false, false},
	} {
		manager.uploadsDisabledHandler(tc.disable)(context.Background(), nil, &models.Update{Message: &models.Message{Chat: models.Chat{ID: tc.id}}})
		if got := !manager.control.enabled(); got != tc.want {
			t.Errorf("user %d setting disabled=%v: got %v, want %v", tc.id, tc.disable, got, tc.want)
		}
	}
}

func readUploadEvents(t *testing.T, manager *Manager) (uploadStarted, uploadFinished) {
	t.Helper()
	var started uploadStarted
	select {
	case event := <-manager.uploadEvents:
		var ok bool
		started, ok = event.(uploadStarted)
		if !ok || started.uploadID == 0 || started.sent == nil {
			t.Fatalf("invalid start event: %#v", event)
		}
	default:
		t.Fatal("missing start event")
	}
	select {
	case event := <-manager.uploadEvents:
		finished, ok := event.(uploadFinished)
		if !ok || finished.uploadID != started.uploadID || finished.ChatID() != started.ChatID() {
			t.Fatalf("finish event = %#v, start = %#v", event, started)
		}
		if len(manager.uploadEvents) != 0 {
			t.Fatal("unexpected extra upload events")
		}
		return started, finished
	default:
		t.Fatal("missing finish event")
		return started, uploadFinished{}
	}
}

func TestProcessDocumentTracksBytesWithoutProgressEvents(t *testing.T) {
	manager := testManager(context.Background())
	b := testBot(t, fileServer)
	var previousID int64
	for i := 0; i < 2; i++ {
		err := manager.processDocument(context.Background(), b, func(data io.Reader, _, _ string) error {
			_, err := io.Copy(io.Discard, data)
			return err
		}, "alice", 42, &models.Document{FileID: "file-1", FileSize: 13, MimeType: "image/jpeg"})
		if err != nil {
			t.Fatal(err)
		}
		started, finished := readUploadEvents(t, manager)
		if started.sent.Load() != 13 || started.fileSize != 13 || finished.err != nil {
			t.Errorf("start = %+v, bytes = %d, finish = %+v", started, started.sent.Load(), finished)
		}
		if started.uploadID <= previousID {
			t.Fatalf("upload ID %d did not increase from %d", started.uploadID, previousID)
		}
		previousID = started.uploadID
	}
}
