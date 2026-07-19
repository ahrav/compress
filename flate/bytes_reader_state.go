//go:build !nounsafe && !purego && !appengine

package flate

import (
	"bytes"
	"unsafe"
)

// bytesReaderState mirrors `bytes.Reader`'s unexported layout: backing slice,
// byte index, and previous-rune index. The fast Huffman path reinterprets
// `*bytes.Reader` through this type, so field order and representation must
// match `bytes.Reader`.
type bytesReaderState struct {
	s        []byte
	i        int64
	prevRune int
}

// bytesReaderStateOf reinterprets r's memory as a bytesReaderState. The
// returned pointer aliases the same allocation as r, so it keeps that
// allocation reachable on its own; no runtime.KeepAlive is required at call
// sites.
func bytesReaderStateOf(r *bytes.Reader) *bytesReaderState {
	// `TestBytesReaderStateOf` guards size, field layout, direct slice access,
	// byte-index mutation, and `prevRune` invalidation after `ReadByte`.
	return (*bytesReaderState)(unsafe.Pointer(r))
}
