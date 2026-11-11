package imagespull

import (
	"bytes"
	"strings"
	"testing"
)

// TestCappedWriter_failsOnOverflow asserts an oversize body is rejected (not
// silently truncated into a corrupt image on disk).
func TestCappedWriter_failsOnOverflow(t *testing.T) {
	var out bytes.Buffer
	dst := &cappedWriter{w: &out, max: 10}
	if _, err := dst.Write([]byte("123456")); err != nil {
		t.Fatalf("first chunk under the cap: %v", err)
	}
	_, err := dst.Write([]byte("78901")) // 11 bytes in total, cap 10
	if err == nil {
		t.Fatal("cappedWriter must error when the source exceeds the cap")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should mention the cap: %v", err)
	}
	if out.Len() != 6 {
		t.Errorf("the refused chunk reached the destination: %d bytes, want 6", out.Len())
	}
}

// TestCappedWriter_passesAtOrUnderCap asserts a body at exactly the cap is
// passed through in full with no error.
func TestCappedWriter_passesAtOrUnderCap(t *testing.T) {
	var out bytes.Buffer
	dst := &cappedWriter{w: &out, max: 10}
	if _, err := dst.Write([]byte("1234567890")); err != nil { // exactly 10
		t.Fatalf("a body at the cap should pass: %v", err)
	}
	if out.Len() != 10 {
		t.Errorf("got %d bytes, want 10", out.Len())
	}
}
