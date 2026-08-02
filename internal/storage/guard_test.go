package storage

import (
	"os"
	"strings"
	"testing"
)

// "I could not check" must never read as "there is room". An unreadable mount
// is exactly when you least want to start writing to it.
func TestUnreadablePathRefusesTheGrab(t *testing.T) {
	for _, p := range []string{"", "/definitely/not/a/mount/anywhere"} {
		if err := (Guard{Path: p, FloorBytes: 1 << 30}).Admit(1 << 20); err == nil {
			t.Errorf("path %q admitted a grab despite being uncheckable", p)
		}
	}
}

func TestFloorAndHeadroom(t *testing.T) {
	dir := os.TempDir()
	free, err := Guard{Path: dir}.Free()
	if err != nil {
		t.Skipf("statfs unavailable: %v", err)
	}
	if free <= 0 {
		t.Fatalf("free = %d", free)
	}
	// A floor above actual free space must refuse.
	err = Guard{Path: dir, FloorBytes: free + (1 << 40)}.Admit(1 << 20)
	if err == nil || !strings.Contains(err.Error(), "floor") {
		t.Errorf("floor not enforced: %v", err)
	}
	// A download larger than free space must refuse even under the floor.
	err = Guard{Path: dir, FloorBytes: 1}.Admit(free + (1 << 40))
	if err == nil || !strings.Contains(err.Error(), "needs") {
		t.Errorf("size check not enforced: %v", err)
	}
	// Headroom must be reserved: a grab that exactly fits is still refused.
	err = Guard{Path: dir, FloorBytes: 1, HeadroomBytes: free}.Admit(free / 2)
	if err == nil {
		t.Error("headroom was not reserved — a grab could fill the disk completely")
	}
	// A small grab with room must pass.
	if err := (Guard{Path: dir, FloorBytes: 1, HeadroomBytes: 0}).Admit(1024); err != nil {
		t.Errorf("a 1 KB grab with %d free was refused: %v", free, err)
	}
}
