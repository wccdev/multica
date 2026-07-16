//go:build unix

package agent

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCursorExecuteStopsAfterTerminalResult(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-terminal"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sess-terminal"}'
sleep 10
`
	result := executeFakeCursor(t, script)

	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed; error=%q", result.Status, result.Error)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if result.SessionID != "sess-terminal" {
		t.Fatalf("session id = %q, want sess-terminal", result.SessionID)
	}
}

func TestCursorExecuteEmitsTerminalResultText(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-result-text"}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"final-only answer","session_id":"sess-result-text"}'
`
	fakePath := filepath.Join(t.TempDir(), "cursor-agent")
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("cursor", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("New(cursor): %v", err)
	}
	session, err := backend.Execute(t.Context(), "hello", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var messages []Message
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range session.Messages {
			messages = append(messages, msg)
		}
	}()

	result := <-session.Result
	<-done

	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed; error=%q", result.Status, result.Error)
	}
	if result.Output != "final-only answer" {
		t.Fatalf("output = %q, want final-only answer", result.Output)
	}
	for _, msg := range messages {
		if msg.Type == MessageText && msg.Content == "final-only answer" {
			return
		}
	}
	t.Fatalf("expected terminal result text in message stream, got %+v", messages)
}

func TestCursorExecuteStopsAfterTerminalErrorResult(t *testing.T) {
	t.Parallel()

	script := `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-terminal-error"}'
printf '%s\n' '{"type":"result","subtype":"error","is_error":true,"result":"failed hard","session_id":"sess-terminal-error"}'
sleep 10
`
	result := executeFakeCursor(t, script)

	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed; error=%q", result.Status, result.Error)
	}
	if result.Error != "failed hard" {
		t.Fatalf("error = %q, want failed hard", result.Error)
	}
	if result.Output != "failed hard" {
		t.Fatalf("output = %q, want failed hard", result.Output)
	}
	if result.SessionID != "sess-terminal-error" {
		t.Fatalf("session id = %q, want sess-terminal-error", result.SessionID)
	}
}

func TestCursorExecuteSurfacesStderrWhenChildExitsEarly(t *testing.T) {
	t.Parallel()

	// Fake cursor-agent binary that writes a crash message to stderr, then
	// exits non-zero before emitting any stream-json "result" event. This is
	// the exact failure mode that produced the opaque "cursor-agent exited
	// with error: exit status 1" — without sampling stderrBuf.Tail() after
	// cmd.Wait() returns, the real cause was only visible in daemon logs.
	script := "#!/bin/sh\n" +
		"echo 'panic: remote runtime lost connection' >&2\n" +
		"exit 1\n"
	fakePath := filepath.Join(t.TempDir(), "cursor-agent")
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("cursor", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("New(cursor): %v", err)
	}
	session, err := backend.Execute(t.Context(), "hello", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	result := <-session.Result
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed; error=%q", result.Status, result.Error)
	}
	if !strings.Contains(result.Error, "cursor-agent exited with error") {
		t.Fatalf("expected error to mention exit, got %q", result.Error)
	}
	if !strings.Contains(result.Error, "remote runtime lost connection") {
		t.Fatalf("expected error to include stderr hint, got %q", result.Error)
	}
	if !strings.Contains(result.Error, "cursor stderr:") {
		t.Fatalf("expected stderr label in error, got %q", result.Error)
	}
}

func executeFakeCursor(t *testing.T, script string) Result {
	t.Helper()

	fakePath := filepath.Join(t.TempDir(), "cursor-agent")
	writeTestExecutable(t, fakePath, []byte(script))

	backend, err := New("cursor", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("New(cursor): %v", err)
	}
	session, err := backend.Execute(t.Context(), "hello", ExecOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	result := <-session.Result
	if result.Status == "timeout" {
		t.Fatalf("cursor backend timed out instead of stopping after terminal result; error=%q", result.Error)
	}
	return result
}
