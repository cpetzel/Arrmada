package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// A manual import whose every name is taken by files Arrmada doesn't track is a conflict
// on disk, said plainly — not a 502 as if a service had failed.
func TestManualImportUntrackedTargetIsConflict(t *testing.T) {
	err := fmt.Errorf("%w: /movies/Heat (1995)/Heat (1995) - Default (99).mkv", library.ErrUntrackedTarget)
	code, msg := manualImportError(err)
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if !strings.Contains(msg, "rename or move") {
		t.Fatalf("message %q doesn't say what to do", msg)
	}
	if code, _ := manualImportError(fmt.Errorf("%w: x", library.ErrProtectedTarget)); code != http.StatusConflict {
		t.Fatalf("protected target: %d", code)
	}
}
