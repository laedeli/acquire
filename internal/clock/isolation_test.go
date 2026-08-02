package clock

import (
	"os/exec"
	"strings"
	"testing"
)

// The clock decides nothing: it advances due rows and emits. That is only true
// as long as it cannot reach the domain, so the constraint is enforced rather
// than documented. The day this test fails, somebody has started re-implementing
// acquisition logic inside the timer.
func TestClockDoesNotImportTheDomain(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/laedeli/acquire/internal/app" {
			t.Fatal("internal/clock imports internal/app — the clock must emit, not execute")
		}
	}
}
