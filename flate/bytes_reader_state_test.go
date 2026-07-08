package flate

import (
	"bytes"
	"testing"
	"unsafe"
)

func TestBytesReaderStateOf(t *testing.T) {
	// Guard the unsafe mirror against `bytes.Reader` changes in size, alignment,
	// backing-slice layout, byte-index layout, or `ReadByte`'s `prevRune` rule.
	if got, want := unsafe.Sizeof(bytes.Reader{}), unsafe.Sizeof(bytesReaderState{}); got != want {
		t.Fatalf("bytes.Reader size = %d, mirror size = %d", got, want)
	}
	if got, want := unsafe.Alignof(bytes.Reader{}), unsafe.Alignof(bytesReaderState{}); got != want {
		t.Fatalf("bytes.Reader align = %d, mirror align = %d", got, want)
	}

	r := bytes.NewReader([]byte("abc"))
	state := bytesReaderStateOf(r)
	if got := string(state.s); got != "abc" {
		t.Fatalf("state.s = %q, want %q", got, "abc")
	}
	if state.i != 0 {
		t.Fatalf("state.i = %d, want 0", state.i)
	}

	state.i = 1
	rn, size, err := r.ReadRune()
	if err != nil {
		t.Fatalf("ReadRune: %v", err)
	}
	if rn != 'b' || size != 1 {
		t.Fatalf("ReadRune after state.i mutation = %q/%d, want %q/%d", rn, size, 'b', 1)
	}
	if state.i != 2 {
		t.Fatalf("state.i after ReadRune = %d, want 2", state.i)
	}
	if state.prevRune != 1 {
		t.Fatalf("state.prevRune after ReadRune = %d, want 1", state.prevRune)
	}

	b, err := r.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte: %v", err)
	}
	if b != 'c' {
		t.Fatalf("ReadByte after ReadRune = %q, want %q", b, 'c')
	}
	if state.i != 3 {
		t.Fatalf("state.i after ReadByte = %d, want 3", state.i)
	}
	if state.prevRune >= 0 {
		t.Fatalf("state.prevRune after ReadByte = %d, want < 0", state.prevRune)
	}
}
