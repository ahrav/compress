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

func bytesReaderStateOf(r *bytes.Reader) *bytesReaderState {
	// `TestBytesReaderStateOf` guards size, alignment, direct slice access,
	// byte-index mutation, and `prevRune` invalidation after `ReadByte`.
	return (*bytesReaderState)(unsafe.Pointer(r))
}
