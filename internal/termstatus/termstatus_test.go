package termstatus

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		r    Report
		want string
	}{
		{
			name: "root working with progress",
			r:    Report{State: Working, Progress: Percent(40), Msg: "hook 2/5"},
			want: "\x1b]7501;state=working:progress=40:app=wtx:msg=" + b64("hook 2/5") + "\x1b\\",
		},
		{
			name: "indeterminate working omits progress",
			r:    Report{State: Working},
			want: "\x1b]7501;state=working:app=wtx\x1b\\",
		},
		{
			name: "progress is only sent while working",
			r:    Report{State: Done, Progress: Percent(100), Msg: "ready"},
			want: "\x1b]7501;state=done:app=wtx:msg=" + b64("ready") + "\x1b\\",
		},
		{
			name: "child with title",
			r:    Report{ID: "a-1234", State: Error, Title: "feature/a", Msg: "boom"},
			want: "\x1b]7501;state=error:id=a-1234:app=wtx:title=" + b64("feature/a") + ":msg=" + b64("boom") + "\x1b\\",
		},
		{
			name: "clear carries only the id",
			r:    Report{ID: "a-1234", State: Clear, Msg: "ignored"},
			want: "\x1b]7501;state=clear:id=a-1234\x1b\\",
		},
		{
			name: "progress is clamped",
			r:    Report{State: Working, Progress: Percent(250)},
			want: "\x1b]7501;state=working:progress=100:app=wtx\x1b\\",
		},
		{
			name: "msg keeps its first line and drops control characters",
			r:    Report{State: Error, Msg: "bad\tthing\nsecond line"},
			want: "\x1b]7501;state=error:app=wtx:msg=" + b64("badthing") + "\x1b\\",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encode(tt.r); got != tt.want {
				t.Errorf("encode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanLimits(t *testing.T) {
	long := strings.Repeat("é", 200) // 400 bytes
	if got := clean(long, maxTitle); len(got) != maxTitle {
		t.Errorf("clean title = %d bytes, want %d", len(got), maxTitle)
	}
	if got := clean(strings.Repeat("x", 5000), maxMsg); len(got) != maxMsg {
		t.Errorf("clean msg = %d bytes, want %d", len(got), maxMsg)
	}
	if seq := encode(Report{State: Error, Title: strings.Repeat("t", 999), Msg: strings.Repeat("m", 9999)}); len(seq) > 4096 {
		t.Errorf("largest report is %d bytes, over the 4096 limit", len(seq))
	}
}

func TestEncodeConEmu(t *testing.T) {
	tests := []struct {
		r    Report
		want string
	}{
		{Report{State: Working, Progress: Percent(30)}, "\x1b]9;4;1;30\x1b\\"},
		{Report{State: Working}, "\x1b]9;4;3\x1b\\"},
		{Report{State: Error}, "\x1b]9;4;2;100\x1b\\"},
		{Report{State: Done}, "\x1b]9;4;0\x1b\\"},
		{Report{State: Idle}, "\x1b]9;4;0\x1b\\"},
	}
	for _, tt := range tests {
		if got := encodeConEmu(tt.r); got != tt.want {
			t.Errorf("encodeConEmu(%s) = %q, want %q", tt.r.State, got, tt.want)
		}
	}
}

func TestSlug(t *testing.T) {
	a := Slug("feature/auth", "/p/worktrees/feature/auth")
	if !strings.HasPrefix(a, "feature-auth-") {
		t.Errorf("Slug = %q, want a feature-auth- prefix", a)
	}
	if b := Slug("feature-auth", "/p/worktrees/feature-auth"); a == b {
		t.Errorf("names that sanitize alike share a segment: %q", a)
	}
	long := Slug(strings.Repeat("ü", 40), "x")
	if len(long) > maxSegment {
		t.Errorf("Slug = %d bytes, over the %d-byte segment limit", len(long), maxSegment)
	}
	for _, c := range []byte(long) {
		if !isIDRune(rune(c)) {
			t.Fatalf("Slug %q contains invalid byte %q", long, c)
		}
	}
}

func TestReportDedupesAndMirrors(t *testing.T) {
	var buf bytes.Buffer
	term := newTerminal(&buf, true)

	term.Report(Report{State: Working, Progress: Percent(50)})
	term.Report(Report{State: Working, Progress: Percent(50)})
	if got := strings.Count(buf.String(), "\x1b]7501;"); got != 1 {
		t.Fatalf("a repeated report was written %d times, want 1", got)
	}
	if !strings.HasPrefix(buf.String(), "\x1b]9;4;1;50\x1b\\") {
		t.Errorf("root report is not mirrored to OSC 9;4 first: %q", buf.String())
	}

	buf.Reset()
	term.Report(Report{ID: "child", State: Working})
	if strings.Contains(buf.String(), "\x1b]9;4;") {
		t.Errorf("a child record was mirrored to OSC 9;4: %q", buf.String())
	}

	buf.Reset()
	term.Report(Report{ID: "child", State: Clear})
	term.Report(Report{ID: "child", State: Working})
	if got := strings.Count(buf.String(), "\x1b]7501;"); got != 2 {
		t.Errorf("a report after clear was deduped: %q", buf.String())
	}
}

func TestDisabledTerminalWritesNothing(t *testing.T) {
	var nilTerm *Terminal
	nilTerm.Report(Report{State: Working})
}

func TestDetect(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	term := Detect(f, func(string) string { return "" })
	if term != nil {
		t.Error("Detect enabled a terminal for a regular file")
	}
}

func TestDetectEnv(t *testing.T) {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no controlling terminal")
	}
	defer func() { _ = tty.Close() }()
	env := func(term, status string) func(string) string {
		return func(k string) string {
			return map[string]string{"TERM": term, "WTX_TERMINAL_STATUS": status}[k]
		}
	}
	if Detect(tty, env("xterm", "")) == nil {
		t.Error("Detect disabled a terminal by default")
	}
	if Detect(tty, env("dumb", "")) != nil {
		t.Error("Detect enabled TERM=dumb")
	}
	if Detect(tty, env("xterm", "off")) != nil {
		t.Error("Detect ignored WTX_TERMINAL_STATUS=off")
	}
	if !Detect(tty, env("xterm", "all")).conEmu {
		t.Error("WTX_TERMINAL_STATUS=all did not enable OSC 9;4")
	}
}
