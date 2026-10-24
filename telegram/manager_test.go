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
