package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
			}, "alice", 42, &models.Document{FileID: "file-1", FileName: "photo.jpg", FileSize: 13, MimeType: tc.mime})
			if tc.want {
				if err != nil || !called {
					t.Fatalf("accepted document: upload called = %v, error = %v", called, err)
				}
				started, finished := readUploadEvents(t, manager)
				if started.ChatID() != 42 || started.username != "alice" || started.fileSize != 13 || started.sent.Load() != 0 {
					t.Errorf("start event = %+v, sent = %d", started, started.sent.Load())
				}
				if finished.err != nil {
					t.Errorf("finish error = %v", finished.err)
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
	started, finished := readUploadEvents(t, manager)
	if started.ChatID() != 42 || started.username != "alice" || started.sent.Load() != 0 {
		t.Errorf("start event = %+v", started)
	}
	if !errors.Is(finished.err, want) {
		t.Errorf("finish error = %v, want %v", finished.err, want)
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

type progressCall struct {
	method, chatID, messageID, text string
	failed                          bool
}

func startProgressWatcher(t *testing.T, reject func(progressCall) bool) (chan uploadEvent, <-chan progressCall) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	manager := testManager(ctx)
	calls := make(chan progressCall, 64)
	manager.bot = testBot(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		call := progressCall{
			method: strings.TrimPrefix(r.URL.Path, "/bottest-token/"),
			chatID: r.FormValue("chat_id"), messageID: r.FormValue("message_id"), text: r.FormValue("text"),
		}
		call.failed = reject != nil && reject(call)
		if call.failed {
			_, _ = io.WriteString(w, `{"ok":false,"error_code":500,"description":"temporary failure"}`)
		} else {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
		}
		calls <- call
	})
	events := make(chan uploadEvent, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.watchChatUploads(ctx, 42, "alice", events)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("progress watcher did not stop after cancellation")
		}
	})
	return events, calls
}

func expectProgressCall(t *testing.T, calls <-chan progressCall, method, chatID, text string, failed bool) {
	t.Helper()
	select {
	case call := <-calls:
		if call.method != method || call.chatID != chatID || call.text != text || call.failed != failed {
			t.Fatalf("call = %+v, want %s to %s: %q (failed=%v)", call, method, chatID, text, failed)
		}
		if method == "editMessageText" && call.messageID != "7" {
			t.Fatalf("edited message %q, want 7", call.messageID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s: %q", method, text)
	}
}

func TestWatchChatUploadsProgressAndReset(t *testing.T) {
	events, calls := startProgressWatcher(t, nil)
	first, second := &atomic.Int64{}, &atomic.Int64{}
	events <- uploadStarted{chatID: 42, uploadID: 1, fileSize: 1 << 20, sent: first}
	events <- uploadStarted{chatID: 42, uploadID: 2, fileSize: 1 << 20, sent: second}
	expectProgressCall(t, calls, "sendMessage", "42", "Uploading...\r\n[░░░░░░░░░░] 0.00/2.00MB", false)

	first.Store(1 << 20)
	second.Store(1 << 20)
	expectProgressCall(t, calls, "editMessageText", "42", "Uploading...\r\n[██████████] 2.00/2.00MB", false)
	// Multiple ticks at full byte progress must neither complete nor resend unchanged text.
	select {
	case call := <-calls:
		t.Fatalf("unexpected call before finish events: %+v", call)
	case <-time.After(1200 * time.Millisecond):
	}
	events <- uploadFinished{chatID: 42, uploadID: 1}
	events <- uploadFinished{chatID: 42, uploadID: 2, err: errors.New("album update failed")}
	expectProgressCall(t, calls, "editMessageText", "42", "✅ 1 files uploaded successfully\n❌ 1 files failed", false)
	expectProgressCall(t, calls, "sendMessage", "1234567890", "Uploads from alice: 1 succeeded, 1 failed", false)

	// A later batch gets its own message and excludes the previous batch's counts.
	events <- uploadStarted{chatID: 42, uploadID: 3, fileSize: 1 << 20, sent: &atomic.Int64{}}
	expectProgressCall(t, calls, "sendMessage", "42", "Uploading...\r\n[░░░░░░░░░░] 0.00/1.00MB", false)
	events <- uploadFinished{chatID: 42, uploadID: 3}
	expectProgressCall(t, calls, "editMessageText", "42", "✅ 1 files uploaded successfully", false)
	expectProgressCall(t, calls, "sendMessage", "1234567890", "Uploads from alice: 1 succeeded, 0 failed", false)
}

func TestWatchChatUploadsRetriesInitialSendAfterFinish(t *testing.T) {
	rejected := false
	events, calls := startProgressWatcher(t, func(call progressCall) bool {
		if call.chatID == "42" && !rejected {
			rejected = true
			return true
		}
		return false
	})
	events <- uploadStarted{chatID: 42, uploadID: 1, sent: &atomic.Int64{}}
	events <- uploadFinished{chatID: 42, uploadID: 1, err: errors.New("download failed")}
	const result = "✅ 0 files uploaded successfully\n❌ 1 files failed"
	expectProgressCall(t, calls, "sendMessage", "42", result, true)
	// No further upload events arrive to trigger the retry.
	expectProgressCall(t, calls, "sendMessage", "42", result, false)
	expectProgressCall(t, calls, "sendMessage", "1234567890", "Uploads from alice: 0 succeeded, 1 failed", false)
}

func TestWatchChatUploadsRetriesCompletionEdit(t *testing.T) {
	rejected := false
	events, calls := startProgressWatcher(t, func(call progressCall) bool {
		if call.method == "editMessageText" && !rejected {
			rejected = true
			return true
		}
		return false
	})
	events <- uploadStarted{chatID: 42, uploadID: 1, sent: &atomic.Int64{}}
	expectProgressCall(t, calls, "sendMessage", "42", "Uploading...\r\n[░░░░░░░░░░] 0.00/0.00MB", false)
	events <- uploadFinished{chatID: 42, uploadID: 1}
	expectProgressCall(t, calls, "editMessageText", "42", "✅ 1 files uploaded successfully", true)
	expectProgressCall(t, calls, "editMessageText", "42", "✅ 1 files uploaded successfully", false)
	expectProgressCall(t, calls, "sendMessage", "1234567890", "Uploads from alice: 1 succeeded, 0 failed", false)
}
