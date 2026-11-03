// Package termstatus tells the terminal what wtx is doing, using the OSC 7501
// Program Status Protocol (https://www.superlogical.com/rex/docs/build/program-status),
// optionally mirrored to ConEmu's OSC 9;4 progress bar. Callers build Reports;
// this package owns the encoding, the protocol's limits, deduplication and the
// decision whether escapes may be written at all, so no caller checks for a TTY.
package termstatus

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	isatty "github.com/mattn/go-isatty"
)

// State is a record's state. blocked is left out: wtx never waits on the user.
type State string

const (
	Idle    State = "idle"
	Working State = "working"
	Done    State = "done"
	Error   State = "error"
	// Clear removes the addressed record and the records beneath it.
	Clear State = "clear"
)

// Progress is a percentage or indeterminate. The zero value is indeterminate,
// so a Report{State: Working} never claims 0%.
type Progress struct {
	pct   int
	known bool
}

// Percent returns a known progress, clamped to 0..100.
func Percent(n int) Progress {
	return Progress{pct: min(max(n, 0), 100), known: true}
}

// Report replaces the record at ID completely. ID "" is the root record, which
// a program that reports only its own state uses. Title and Msg are free text;
// control characters are dropped and they are cut to the protocol's limits.
type Report struct {
	ID       string
	State    State
	Progress Progress // encoded only for Working
	Title    string
	Msg      string
}

const (
	app          = "wtx"
	maxTitle     = 192
	maxMsg       = 2048
	maxSegment   = 32
	slugHashLen  = 4
	sequenceHead = "\x1b]7501;"
	terminator   = "\x1b\\"
)

// Slug turns text (such as a branch name) into one valid record id segment.
// Bytes outside [A-Za-z0-9_.+-] become '-', and a short hash of salt is
// appended, so two names that sanitize alike still get different segments.
func Slug(text, salt string) string {
	var b strings.Builder
	for _, r := range text {
		if isIDRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(salt))
	suffix := fmt.Sprintf("-%0*x", slugHashLen, h.Sum32()&0xffff)
	s := b.String()
	if limit := maxSegment - len(suffix); len(s) > limit {
		s = s[:limit]
	}
	return s + suffix
}

func isIDRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '.' || c == '+' || c == '-'
}

// Terminal writes Reports to one terminal from one goroutine. A nil
// *Terminal is disabled: it accepts every call and writes nothing.
type Terminal struct {
	w      io.Writer
	conEmu bool
	last   map[string]string
}

// Detect returns a Terminal for f, which should be os.Stderr: stdout carries
// machine output, and ui.Output may be a log file. It is disabled unless f is
// a terminal and TERM is not "dumb". WTX_TERMINAL_STATUS=off (or 0, false)
// disables it; =all also mirrors the root record to OSC 9;4.
func Detect(f *os.File, getenv func(string) string) *Terminal {
	if f == nil || !isatty.IsTerminal(f.Fd()) && !isatty.IsCygwinTerminal(f.Fd()) || getenv("TERM") == "dumb" {
		return nil
	}
	switch strings.ToLower(getenv("WTX_TERMINAL_STATUS")) {
	case "off", "0", "false":
		return nil
	case "all":
		return newTerminal(f, true)
	default:
		return newTerminal(f, false)
	}
}

func newTerminal(w io.Writer, conEmu bool) *Terminal {
	return &Terminal{w: w, conEmu: conEmu, last: map[string]string{}}
}

// Report sends r unless it repeats the last report for r.ID. Each sequence
// goes out in one Write so it cannot interleave with other output. Write
// errors are ignored: terminal status is best-effort.
func (t *Terminal) Report(r Report) {
	if t == nil {
		return
	}
	seq := encode(r)
	if r.State == Clear {
		delete(t.last, r.ID)
	} else {
		if t.last[r.ID] == seq {
			return
		}
		t.last[r.ID] = seq
	}
	if t.conEmu && r.ID == "" {
		seq = encodeConEmu(r) + seq
	}
	_, _ = io.WriteString(t.w, seq)
}

// encode returns the OSC 7501 sequence for r.
func encode(r Report) string {
	pairs := []string{"state=" + string(r.State)}
	if r.ID != "" {
		pairs = append(pairs, "id="+r.ID)
	}
	if r.State != Clear {
		if r.State == Working && r.Progress.known {
			pairs = append(pairs, fmt.Sprintf("progress=%d", r.Progress.pct))
		}
		pairs = append(pairs, "app="+app)
		if title := clean(r.Title, maxTitle); title != "" {
			pairs = append(pairs, "title="+base64.StdEncoding.EncodeToString([]byte(title)))
		}
		if msg := clean(r.Msg, maxMsg); msg != "" {
			pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(msg)))
		}
	}
	return sequenceHead + strings.Join(pairs, ":") + terminator
}

// encodeConEmu maps a root report to OSC 9;4. An error stays up so the user
// sees it; done, idle and clear remove the bar.
func encodeConEmu(r Report) string {
	var body string
	switch {
	case r.State == Working && r.Progress.known:
		body = fmt.Sprintf("1;%d", r.Progress.pct)
	case r.State == Working:
		body = "3"
	case r.State == Error:
		body = "2;100"
	default:
		body = "0"
	}
	return "\x1b]9;4;" + body + terminator
}

// clean keeps the first line of s, drops control characters, and cuts it to
// at most limit bytes without splitting a rune.
func clean(s string, limit int) string {
	s, _, _ = strings.Cut(s, "\n")
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == utf8.RuneError {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
