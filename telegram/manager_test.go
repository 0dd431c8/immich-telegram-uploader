package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
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

func TestStreamFileToUpload(t *testing.T) {
	b := testBot(t, fileServer)
	called := false
	err := streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", func(data io.Reader, filename, author string) error {
		called = true
		content, err := io.ReadAll(data)
		if err != nil {
			return err
		}
		if string(content) != "media content" || filename != "photo.jpg" || author != "alice" {
			t.Errorf("upload = (%q, %q, %q)", content, filename, author)
		}
		return nil
	}, nil)
	if err != nil || !called {
		t.Fatalf("upload called = %v, error = %v", called, err)
	}
}

func TestStreamFileToUploadFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		getFileErr bool
		status     int
	}{
		{name: "getFile rejected", getFileErr: true},
		{name: "download not found", status: http.StatusNotFound},
		{name: "download server failure", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBot(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/bottest-token/getFile" {
					if tc.getFileErr {
						_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"file unavailable"}`)
						return
					}
					fileServer(w, r)
					return
				}
				http.Error(w, "download failed", tc.status)
			})
			called := false
			err := streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", func(io.Reader, string, string) error {
				called = true
				return nil
			}, nil)
			if err == nil {
				t.Error("expected error for failed Telegram request")
			}
			if called {
				t.Error("upload callback called after failed Telegram request")
			}
		})
	}
}

func TestStreamFileToUploadPropagatesUploadError(t *testing.T) {
	b := testBot(t, fileServer)
	want := errors.New("Immich unavailable")
	err := streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", func(io.Reader, string, string) error { return want }, nil)
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestStreamFileToUploadClosesDownload(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		uploadErr error
	}{
		{name: "success", status: http.StatusOK},
		{name: "download failure", status: http.StatusNotFound},
		{name: "upload failure", status: http.StatusOK, uploadErr: errors.New("upload failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBot(t, fileServer)
			body := &trackedBody{Reader: strings.NewReader("media content")}
			original := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Status: fmt.Sprintf("%d %s", tc.status, http.StatusText(tc.status)), Body: body, Header: make(http.Header)}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = original })
			_ = streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", func(io.Reader, string, string) error { return tc.uploadErr }, nil)
			if !body.closed {
				t.Error("download response body was not closed")
			}
		})
	}
}

func TestStreamFileToUploadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := testBot(t, func(w http.ResponseWriter, r *http.Request) {
		fileServer(w, r)
		// Cancel after getFile so the download must honor cancellation too.
		cancel()
	})
	called := false
	err := streamFileToUpload(ctx, b, "file-1", "photo.jpg", 13, "alice", func(io.Reader, string, string) error { called = true; return nil }, nil)
	if !errors.Is(err, context.Canceled) || called {
		t.Errorf("upload called = %v, error = %v; want cancellation without upload", called, err)
	}
}

func TestProcessDocumentFiltering(t *testing.T) {
	for _, tc := range []struct {
		mime string
		want bool
	}{
		{"image/jpeg", true}, {"image/png", true}, {"image/heic", true},
		{"video/mp4", true}, {"video/quicktime", true},
		{"application/pdf", false}, {"application/octet-stream", false},
		{"text/plain", false}, {"image", false}, {"", false},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			manager := testManager(context.Background())
			b := testBot(t, fileServer)
			called := false
			err := manager.processDocument(context.Background(), b, func(io.Reader, string, string) error {
				called = true
				return nil
			}, "alice", 42, &models.Document{FileID: "file-1", FileName: "photo.jpg", MimeType: tc.mime})
			if tc.want {
				if err != nil || !called {
					t.Fatalf("accepted document: upload called = %v, error = %v", called, err)
				}
				for _, want := range []uploadEvent{uploadStarted{id: 42, username: "alice"}, uploadFinished{id: 42}} {
					select {
					case event := <-manager.uploadEvents:
						if event != want {
							t.Errorf("event = %#v, want %#v", event, want)
						}
					default:
						t.Fatal("missing upload event")
					}
				}
			} else if err == nil || called || len(manager.uploadEvents) != 0 {
				t.Errorf("unsupported document: upload called = %v, error = %v, events = %d", called, err, len(manager.uploadEvents))
			}
		})
	}
}

func TestProcessDocumentFinishesAfterUploadFailure(t *testing.T) {
	manager := testManager(context.Background())
	b := testBot(t, fileServer)
	want := errors.New("upload failed")
	err := manager.processDocument(context.Background(), b, func(io.Reader, string, string) error { return want }, "alice", 42, &models.Document{FileID: "file-1", MimeType: "image/jpeg"})
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
	for _, want := range []uploadEvent{uploadStarted{id: 42, username: "alice"}, uploadFinished{id: 42}} {
		select {
		case event := <-manager.uploadEvents:
			if event != want {
				t.Errorf("event = %#v, want %#v", event, want)
			}
		default:
			t.Fatal("missing upload event after failure")
		}
	}
}

func TestProcessDocumentCancellationWithFullEventQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager := testManager(ctx)
	for i := 0; i < cap(manager.uploadEvents); i++ {
		manager.uploadEvents <- uploadStarted{}
	}
	cancel()
	// A nil bot ensures cancellation returns before any Telegram request.
	err := manager.processDocument(ctx, nil, nil, "alice", 42, &models.Document{MimeType: "image/jpeg"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
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
		if got := manager.uploadsDisabled.Load(); got != tc.want {
			t.Errorf("user %d setting disabled=%v: got %v, want %v", tc.id, tc.disable, got, tc.want)
		}
	}
}

func TestWatchChatUploadsProgressAndReset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := testManager(ctx)
	var mu sync.Mutex
	var calls []string
	completed := make(chan struct{}, 2)
	manager.bot = testBot(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		method := strings.TrimPrefix(r.URL.Path, "/bottest-token/")
		text := r.FormValue("text")
		isAdminNotification := method == "sendMessage" && strings.HasSuffix(text, " uploads from alice")
		wantChatID := "42"
		if isAdminNotification {
			wantChatID = "1234567890"
		}
		if r.FormValue("chat_id") != wantChatID {
			t.Errorf("chat_id = %q, want %s", r.FormValue("chat_id"), wantChatID)
		}
		mu.Lock()
		calls = append(calls, method+":"+r.FormValue("text"))
		mu.Unlock()
		if method == "deleteMessage" {
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		} else {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
		}
		if isAdminNotification {
			completed <- struct{}{}
		}
	})
	events := make(chan uploadEvent, 8)
	for _, event := range []uploadEvent{
		uploadStarted{id: 42, username: "alice"},
		uploadStarted{id: 42, username: "alice"},
		uploadFinished{id: 42},
		uploadFinished{id: 42},
		uploadStarted{id: 42, username: "alice"},
		uploadFinished{id: 42},
	} {
		events <- event
	}
	done := make(chan struct{})
	go func() { defer close(done); manager.watchChatUploads(ctx, 42, "alice", events) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("progress watcher did not stop after cancellation")
		}
	})
	for i := 0; i < 2; i++ {
		select {
		case <-completed:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for batch completion")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"sendMessage:Uploading...",
		"editMessageText:Uploading...\r\n[░░░░░░░░░░] 0/1",
		"editMessageText:Uploading...\r\n[░░░░░░░░░░] 0/2",
		"editMessageText:Uploading...\r\n[█████░░░░░] 1/2",
		"deleteMessage:",
		"sendMessage:✅ 2 files uploaded successfully",
		"sendMessage:2 uploads from alice",
		"sendMessage:Uploading...",
		"editMessageText:Uploading...\r\n[░░░░░░░░░░] 0/1",
		"deleteMessage:",
		"sendMessage:✅ 1 files uploaded successfully",
		"sendMessage:1 uploads from alice",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("progress calls = %q, want %q", calls, want)
	}
}
