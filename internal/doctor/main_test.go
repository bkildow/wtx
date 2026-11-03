package doctor

import (
	"testing"

	"github.com/bkildow/wtx/internal/testenv"
)

// TestMain isolates HOME and WTX_HOME, so no test reads or writes the real
// ~/.wtx (doctor scans it for orphaned projects).
func TestMain(m *testing.M) { testenv.Main(m) }
