package imagespull

import (
	"strings"
	"testing"
)

// TestCappedBuffer_failsOnOverflow asserts an oversize body is rejected (not
// silently truncated into a corrupt image on disk).
func TestCappedBuffer_failsOnOverflow(t *testing.T) {
	dst := &cappedBuffer{max: 10}
	if _, err := dst.Write([]byte("123456")); err != nil {
		t.Fatalf("first chunk under the cap: %v", err)
	}
	_, err := dst.Write([]byte("78901")) // 11 bytes in total, cap 10
	if err == nil {
		t.Fatal("cappedBuffer must error when the source exceeds the cap")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should mention the cap: %v", err)
	}
}

// TestCappedBuffer_passesAtOrUnderCap asserts a body at exactly the cap is
// kept in full with no error.
func TestCappedBuffer_passesAtOrUnderCap(t *testing.T) {
	dst := &cappedBuffer{max: 10}
	if _, err := dst.Write([]byte("1234567890")); err != nil { // exactly 10
		t.Fatalf("a body at the cap should pass: %v", err)
	}
	if dst.buf.Len() != 10 {
		t.Errorf("got %d bytes, want 10", dst.buf.Len())
	}
}
