package cmd

import (
	"io"
	"strings"
	"testing"
)

func TestReadHookPayload_valid(t *testing.T) {
	input := `{"session_id":"abc","hook_event_name":"WorktreeCreate","name":"feature/test","cwd":"/tmp/proj"}`
	payload, err := readHookPayload(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.SessionID != "abc" {
		t.Errorf("session_id = %q, want %q", payload.SessionID, "abc")
	}
	if payload.Name != "feature/test" {
		t.Errorf("name = %q, want %q", payload.Name, "feature/test")
	}
	if payload.Cwd != "/tmp/proj" {
		t.Errorf("cwd = %q, want %q", payload.Cwd, "/tmp/proj")
	}
}

func TestReadHookPayload_empty(t *testing.T) {
	_, err := readHookPayload(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "no payload") {
		t.Errorf("error = %q, want 'no payload'", err.Error())
	}
}

func TestReadHookPayload_invalidJSON(t *testing.T) {
	_, err := readHookPayload(strings.NewReader("{invalid"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error = %q, want 'invalid JSON'", err.Error())
	}
}

func TestReadHookPayload_missingFields(t *testing.T) {
	// Missing fields should not error — they are just empty strings.
	input := `{"session_id":"abc"}`
	payload, err := readHookPayload(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Name != "" {
		t.Errorf("name should be empty, got %q", payload.Name)
	}
}

func TestReadHookPayload_extraFields(t *testing.T) {
	// Extra fields should be silently ignored.
	input := `{"name":"test","unknown_field":"value"}`
	payload, err := readHookPayload(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Name != "test" {
		t.Errorf("name = %q, want %q", payload.Name, "test")
	}
}

func TestReadHookPayload_noEOF(t *testing.T) {
	// Simulate a pipe that delivers JSON but never closes (no EOF).
	// json.NewDecoder should return after parsing the complete object.
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(`{"session_id":"abc","name":"test"}`))
		// Deliberately do NOT close pw — simulates a pipe that stays open.
	}()

	payload, err := readHookPayload(pr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Name != "test" {
		t.Errorf("name = %q, want %q", payload.Name, "test")
	}
	_ = pw.Close()
}

func TestReadHookPayload_timeout(t *testing.T) {
	// Simulate a pipe that is open but never sends any data.
	// readHookPayload should time out instead of blocking forever.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	_, err := readHookPayload(pr)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want timeout message", err.Error())
	}
}

func TestResolveProjectRoot_noProject(t *testing.T) {
	payload := hookPayload{
		Cwd: "/nonexistent/path",
	}
	_, err := resolveProjectRoot(payload)
	if err == nil {
		t.Fatal("expected error for nonexistent paths")
	}
	if !strings.Contains(err.Error(), "could not find wt project root") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestResolveProjectRoot_emptyPayload(t *testing.T) {
	payload := hookPayload{}
	_, err := resolveProjectRoot(payload)
	if err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestClaudeInitDefaultBinary(t *testing.T) {
	cmd := newClaudeInitCmd()
	got, err := cmd.Flags().GetString("binary")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wtx" {
		t.Fatalf("binary = %q, want wtx", got)
	}
}
