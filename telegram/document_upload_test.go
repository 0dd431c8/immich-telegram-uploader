package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

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
