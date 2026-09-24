package config

import (
	"os"
	"testing"
)

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // Restore the original value when the test finishes.
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestLookupEnv(t *testing.T) {
	for _, tt := range []struct {
		name            string
		legacy, current *string
		want            string
	}{
		{name: "neither set"},
		{name: "legacy only", legacy: envValue("old"), want: "old"},
		{name: "current only", current: envValue("new"), want: "new"},
		{name: "current takes precedence", legacy: envValue("old"), current: envValue("new"), want: "new"},
		{name: "explicit empty takes precedence", legacy: envValue("old"), current: envValue("")},
		{name: "empty legacy", legacy: envValue("")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			unsetEnv(t, "WT_THEME")
			unsetEnv(t, "WTX_THEME")
			if tt.legacy != nil {
				t.Setenv("WT_THEME", *tt.legacy)
			}
			if tt.current != nil {
				t.Setenv("WTX_THEME", *tt.current)
			}
			if got := LookupEnv("THEME"); got != tt.want {
				t.Fatalf("LookupEnv = %q, want %q", got, tt.want)
			}
		})
	}
}

func envValue(value string) *string { return &value }
