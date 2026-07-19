//go:build nounsafe || purego || appengine

package flate

// huffmanBytesReaderFast requires the unsafe bytes.Reader mirror, which is
// unavailable under these build tags; delegate to the generated safe decoder.
// Both decoders share the same suspended state and the huffmanBytesReader
// resume token, so dispatch behaves identically.
func (f *decompressor) huffmanBytesReaderFast() {
	f.huffmanBytesReader()
}
