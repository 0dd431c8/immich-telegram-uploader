package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
