package cmd

import (
	"errors"
	"fmt"
)

// exitError is a failure the command already reported that ends wtx with a
// specific exit status instead of 1.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func (e exitError) Unwrap() error { return ErrReported }

// ExitCode is the process exit status for an error returned by Execute.
func ExitCode(err error) int {
	var e exitError
	if errors.As(err, &e) {
		return int(e)
	}
	return 1
}
