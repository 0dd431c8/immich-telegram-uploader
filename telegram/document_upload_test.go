package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestStreamFileToUpload(t *testing.T) {
	b := testBot(t, fileServer)
	called := false
	err := streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", false, func(data io.Reader, filename, author string) error {
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
			err := streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", false, func(io.Reader, string, string) error {
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
	err := streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", false, func(io.Reader, string, string) error { return want }, nil)
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

func TestStreamLocalFileToUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, []byte("media content"), 0600); err != nil {
		t.Fatal(err)
	}
	uploadErr := errors.New("Immich unavailable")
	for _, tc := range []struct {
		name       string
		path       string
		getFileErr bool
		uploadErr  error
		cancel     bool
	}{
		{name: "success", path: path},
		{name: "getFile rejected", getFileErr: true},
		{name: "file missing", path: filepath.Join(t.TempDir(), "missing.jpg")},
		{name: "upload failed", path: path, uploadErr: uploadErr},
		{name: "canceled", path: path, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := testBot(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bottest-token/getFile" {
					t.Errorf("unexpected HTTP download: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if tc.getFileErr {
					_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"file unavailable"}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"file_id":"file-1","file_path":%q}}`, tc.path)
				if tc.cancel {
					cancel()
				}
			})
			called := false
			err := streamFileToUpload(ctx, b, "file-1", "photo.jpg", 13, "alice", true, func(data io.Reader, filename, author string) error {
				called = true
				content, err := io.ReadAll(data)
				if err != nil {
					return err
				}
				if string(content) != "media content" || filename != "photo.jpg" || author != "alice" {
					t.Errorf("upload = (%q, %q, %q)", content, filename, author)
				}
				return tc.uploadErr
			}, nil)
			wantCall := !tc.getFileErr && !tc.cancel && tc.path == path
			wantError := !wantCall || tc.uploadErr != nil
			if called != wantCall || (err != nil) != wantError {
				t.Fatalf("upload called = %v, error = %v; want called = %v, error = %v", called, err, wantCall, wantError)
			}
			if tc.uploadErr != nil && !errors.Is(err, tc.uploadErr) {
				t.Errorf("error = %v, want %v", err, tc.uploadErr)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want cancellation", err)
			}
		})
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
			_ = streamFileToUpload(context.Background(), b, "file-1", "photo.jpg", 13, "alice", false, func(io.Reader, string, string) error { return tc.uploadErr }, nil)
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
	err := streamFileToUpload(ctx, b, "file-1", "photo.jpg", 13, "alice", false, func(io.Reader, string, string) error { called = true; return nil }, nil)
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
			reporter := newUploadReporter(nil, 1234567890, slog.New(slog.NewTextHandler(io.Discard, nil)))
			b := testBot(t, fileServer)
			called := false
			uploader := newDocumentUploader(func(io.Reader, string, string) error {
				called = true
				return nil
			}, reporter, false)
			err := uploader.processDocument(context.Background(), b, "alice", 42, &models.Document{FileID: "file-1", FileName: "photo.jpg", FileSize: 13, MimeType: tc.mime})
			if tc.want {
				if err != nil || !called {
					t.Fatalf("accepted document: upload called = %v, error = %v", called, err)
				}
				started, finished := readUploadEvents(t, reporter)
				if started.ChatID() != 42 || started.username != "alice" || started.fileSize != 13 || started.sent.Load() != 0 {
					t.Errorf("start event = %+v, sent = %d", started, started.sent.Load())
				}
				if finished.err != nil {
					t.Errorf("finish error = %v", finished.err)
				}
			} else if err == nil || called || len(reporter.events) != 0 {
				t.Errorf("unsupported document: upload called = %v, error = %v, events = %d", called, err, len(reporter.events))
			}
		})
	}
}

func TestProcessDocumentFinishesAfterUploadFailure(t *testing.T) {
	reporter := newUploadReporter(nil, 1234567890, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b := testBot(t, fileServer)
	want := errors.New("upload failed")
	uploader := newDocumentUploader(func(io.Reader, string, string) error { return want }, reporter, false)
	err := uploader.processDocument(context.Background(), b, "alice", 42, &models.Document{FileID: "file-1", MimeType: "image/jpeg"})
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
	started, finished := readUploadEvents(t, reporter)
	if started.ChatID() != 42 || started.username != "alice" || started.sent.Load() != 0 {
		t.Errorf("start event = %+v", started)
	}
	if !errors.Is(finished.err, want) {
		t.Errorf("finish error = %v, want %v", finished.err, want)
	}
}

func TestProcessDocumentCancellationWithFullEventQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reporter := newUploadReporter(nil, 1234567890, slog.New(slog.NewTextHandler(io.Discard, nil)))
	uploader := newDocumentUploader(nil, reporter, false)
	for i := 0; i < cap(reporter.events); i++ {
		reporter.events <- uploadStarted{}
	}
	cancel()
	// A nil bot ensures cancellation returns before any Telegram request.
	err := uploader.processDocument(ctx, nil, "alice", 42, &models.Document{MimeType: "image/jpeg"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func readUploadEvents(t *testing.T, reporter *uploadReporter) (uploadStarted, uploadFinished) {
	t.Helper()
	var started uploadStarted
	select {
	case event := <-reporter.events:
		var ok bool
		started, ok = event.(uploadStarted)
		if !ok || started.uploadID == 0 || started.sent == nil {
			t.Fatalf("invalid start event: %#v", event)
		}
	default:
		t.Fatal("missing start event")
	}
	select {
	case event := <-reporter.events:
		finished, ok := event.(uploadFinished)
		if !ok || finished.uploadID != started.uploadID || finished.ChatID() != started.ChatID() {
			t.Fatalf("finish event = %#v, start = %#v", event, started)
		}
		if len(reporter.events) != 0 {
			t.Fatal("unexpected extra upload events")
		}
		return started, finished
	default:
		t.Fatal("missing finish event")
		return started, uploadFinished{}
	}
}

func TestProcessDocumentTracksBytesWithoutProgressEvents(t *testing.T) {
	reporter := newUploadReporter(nil, 1234567890, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b := testBot(t, fileServer)
	uploader := newDocumentUploader(func(data io.Reader, _, _ string) error {
		_, err := io.Copy(io.Discard, data)
		return err
	}, reporter, false)
	var previousID int64
	for i := 0; i < 2; i++ {
		err := uploader.processDocument(context.Background(), b, "alice", 42, &models.Document{FileID: "file-1", FileSize: 13, MimeType: "image/jpeg"})
		if err != nil {
			t.Fatal(err)
		}
		started, finished := readUploadEvents(t, reporter)
		if started.sent.Load() != 13 || started.fileSize != 13 || finished.err != nil {
			t.Errorf("start = %+v, bytes = %d, finish = %+v", started, started.sent.Load(), finished)
		}
		if started.uploadID <= previousID {
			t.Fatalf("upload ID %d did not increase from %d", started.uploadID, previousID)
		}
		previousID = started.uploadID
	}
}
