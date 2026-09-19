package cmd

import "os"

// lookupEnv prefers WTX_ variables, falling back to WT_ only when unset.
// An explicitly empty WTX_ value overrides the legacy value.
func lookupEnv(suffix string) string {
	if value, ok := os.LookupEnv("WTX_" + suffix); ok {
		return value
	}
	return os.Getenv("WT_" + suffix)
}
