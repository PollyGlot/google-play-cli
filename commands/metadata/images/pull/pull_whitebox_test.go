package imagespull

import (
	"strings"
	"testing"
)

// TestReadCapped_failsOnOverflow asserts an oversize body is rejected (not
// silently truncated into a corrupt image on disk).
func TestReadCapped_failsOnOverflow(t *testing.T) {
	_, err := readCapped(strings.NewReader("12345678901"), 10) // 11 bytes, cap 10
	if err == nil {
		t.Fatal("readCapped must error when the source exceeds the cap")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should mention the cap: %v", err)
	}
}

// TestReadCapped_passesAtOrUnderCap asserts a body at exactly the cap is read
// in full with no error.
func TestReadCapped_passesAtOrUnderCap(t *testing.T) {
	b, err := readCapped(strings.NewReader("1234567890"), 10) // exactly 10
	if err != nil {
		t.Fatalf("readCapped at the cap should succeed: %v", err)
	}
	if len(b) != 10 {
		t.Errorf("got %d bytes, want 10", len(b))
	}
}
