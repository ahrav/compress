//go:build !nounsafe && !purego && !appengine

package flate

import (
	"bytes"
	"unsafe"
)

// bytesReaderState mirrors `bytes.Reader`'s unexported layout: backing slice,
// byte index, and previous-rune index. A follow-up change lets the fast
// Huffman path reinterpret `*bytes.Reader` through this type, so field order
// and representation must match `bytes.Reader`.
type bytesReaderState struct {
	s        []byte
	i        int64
	prevRune int
}

// bytesReaderLayoutOK reports whether bytesReaderState still matches the
// running toolchain's bytes.Reader layout. It is evaluated once at package
// init. Callers of bytesReaderStateOf must fall back to a safe path when it
// is false: the test-time layout checks never run in consumer binaries, so
// this is the only guard against a future Go release reordering or retyping
// bytes.Reader's unexported fields.
//
// The probe never loads through the mirrored slice's data pointer, so a
// drifted layout cannot fault here; it only compares header words and
// observes byte-index/prevRune transitions driven through bytes.Reader's own
// methods.
var bytesReaderLayoutOK = func() bool {
	if unsafe.Sizeof(bytes.Reader{}) != unsafe.Sizeof(bytesReaderState{}) {
		return false
	}
	data := []byte{7, 42}
	r := bytes.NewReader(data)
	st := (*bytesReaderState)(unsafe.Pointer(r))
	// Slice header at s's offset: length, capacity, and base pointer must
	// all match the backing slice. &st.s[0] is address arithmetic only.
	if len(st.s) != 2 || cap(st.s) != 2 || &st.s[0] != &data[0] {
		return false
	}
	// bytes.NewReader starts at index 0 with no previous rune.
	if st.i != 0 || st.prevRune != -1 {
		return false
	}
	// ReadRune advances i and records the rune's byte index in prevRune.
	if rn, size, err := r.ReadRune(); err != nil || rn != 7 || size != 1 {
		return false
	}
	if st.i != 1 || st.prevRune != 0 {
		return false
	}
	// Mutating the mirrored byte index must steer the next read, and
	// ReadByte must reset prevRune to its -1 sentinel.
	st.i = 0
	b, err := r.ReadByte()
	if err != nil || b != 7 {
		return false
	}
	return st.i == 1 && st.prevRune == -1
}()

// bytesReaderStateOf reinterprets r's memory as a bytesReaderState. Callers
// must check bytesReaderLayoutOK first and use a safe fallback when it is
// false. The returned pointer aliases the same allocation as r, so it keeps
// that allocation reachable on its own; no runtime.KeepAlive is required at
// call sites.
func bytesReaderStateOf(r *bytes.Reader) *bytesReaderState {
	// `TestBytesReaderStateOf` guards size, field layout, direct slice access,
	// byte-index mutation, and `prevRune` invalidation after `ReadByte`.
	return (*bytesReaderState)(unsafe.Pointer(r))
}
