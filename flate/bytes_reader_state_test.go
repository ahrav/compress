//go:build !nounsafe && !purego && !appengine

package flate

import (
	"bytes"
	"reflect"
	"testing"
	"unsafe"
)

func TestBytesReaderStateOf(t *testing.T) {
	// Guard the unsafe mirror against `bytes.Reader` changes in size, field
	// layout, backing-slice access, byte-index tracking, or `ReadByte`'s
	// `prevRune` rule.
	if !bytesReaderLayoutOK {
		t.Fatal("bytesReaderLayoutOK = false; runtime layout probe rejected bytes.Reader mirror")
	}
	if got, want := unsafe.Sizeof(bytes.Reader{}), unsafe.Sizeof(bytesReaderState{}); got != want {
		t.Fatalf("bytes.Reader size = %d, mirror size = %d", got, want)
	}
	// Cross-check every field: same count, same type, same offset. This
	// catches reordering, retyping, or realignment that Sizeof alone misses.
	rt := reflect.TypeOf(bytes.Reader{})
	mt := reflect.TypeOf(bytesReaderState{})
	if rt.NumField() != mt.NumField() {
		t.Fatalf("bytes.Reader has %d fields, mirror has %d", rt.NumField(), mt.NumField())
	}
	for idx := 0; idx < rt.NumField(); idx++ {
		rf, mf := rt.Field(idx), mt.Field(idx)
		if rf.Type != mf.Type {
			t.Fatalf("field %d: bytes.Reader type = %v, mirror type = %v", idx, rf.Type, mf.Type)
		}
		if rf.Offset != mf.Offset {
			t.Fatalf("field %d: bytes.Reader offset = %d, mirror offset = %d", idx, rf.Offset, mf.Offset)
		}
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
	if state.prevRune != -1 {
		t.Fatalf("state.prevRune after ReadByte = %d, want -1", state.prevRune)
	}
}
