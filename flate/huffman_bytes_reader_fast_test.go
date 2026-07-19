package flate

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// diffDecodeResult captures everything the two bytes.Reader Huffman decoders
// must agree on after decoding a stream to completion.
type diffDecodeResult struct {
	output    []byte
	chunkLens []int
	err       error
	roffset   int64
	readerPos int64
	b         uint32
	nb        uint
}

// decodeBytesReader drives a full decode of data using either the reference
// huffmanBytesReader or huffmanBytesReaderFast for every Huffman block step.
// It mirrors decompressor.Read's flush-on-error behavior and records the
// flush-chunk boundaries so pause points must match too.
func decodeBytesReader(data []byte, prefix int, fast bool) diffDecodeResult {
	fr := bytes.NewReader(data)
	if prefix > 0 {
		// Start mid-reader to exercise a non-zero initial byte index.
		if _, err := fr.Seek(int64(prefix), io.SeekStart); err != nil {
			panic(err)
		}
	}
	f := NewReaderOpts(fr).(*decompressor)

	var res diffDecodeResult
	flushed := false
	for {
		if len(f.toRead) > 0 {
			res.output = append(res.output, f.toRead...)
			res.chunkLens = append(res.chunkLens, len(f.toRead))
			f.toRead = nil
			continue
		}
		if f.err != nil {
			if !flushed {
				flushed = true
				f.toRead = f.dict.readFlush()
				continue
			}
			break
		}
		switch f.step {
		case nextBlock:
			diffNextBlock(f, fast)
		case huffmanBytesReader:
			if fast {
				f.huffmanBytesReaderFast()
			} else {
				f.huffmanBytesReader()
			}
		default:
			f.doStep()
		}
	}

	res.err = f.err
	res.roffset = f.roffset
	res.readerPos = fr.Size() - int64(fr.Len())
	res.b = f.b
	res.nb = f.nb
	return res
}

// diffNextBlock is decompressor.nextBlock with the Huffman block decode
// dispatched to the reference or fast bytes.Reader implementation.
func diffNextBlock(f *decompressor, fast bool) {
	for f.nb < 1+2 {
		if f.err = f.moreBits(); f.err != nil {
			return
		}
	}
	f.final = f.b&1 == 1
	f.b >>= 1
	typ := f.b & 3
	f.b >>= 2
	f.nb -= 1 + 2
	decode := func() {
		if fast {
			f.huffmanBytesReaderFast()
		} else {
			f.huffmanBytesReader()
		}
	}
	switch typ {
	case 0:
		f.dataBlock()
	case 1:
		f.hl = &fixedHuffmanDecoder
		f.hd = nil
		decode()
	case 2:
		if f.err = f.readHuffman(); f.err != nil {
			return
		}
		f.hl = &f.h1
		f.hd = &f.h2
		decode()
	default:
		f.err = CorruptInputError(f.roffset)
	}
}

// requireEqualDecode decodes data both ways and fails the test on any
// divergence in output, flush boundaries, error, offsets, or bit buffer.
func requireEqualDecode(t *testing.T, name string, data []byte, prefix int) {
	t.Helper()
	ref := decodeBytesReader(data, prefix, false)
	got := decodeBytesReader(data, prefix, true)

	if !bytes.Equal(ref.output, got.output) {
		t.Fatalf("%s: output mismatch: reference %d bytes, fast %d bytes", name, len(ref.output), len(got.output))
	}
	if !reflect.DeepEqual(ref.chunkLens, got.chunkLens) {
		t.Fatalf("%s: flush-chunk boundaries mismatch: reference %v, fast %v", name, ref.chunkLens, got.chunkLens)
	}
	if !reflect.DeepEqual(ref.err, got.err) {
		t.Fatalf("%s: error mismatch: reference %v, fast %v", name, ref.err, got.err)
	}
	if ref.roffset != got.roffset {
		t.Fatalf("%s: roffset mismatch: reference %d, fast %d", name, ref.roffset, got.roffset)
	}
	if ref.readerPos != got.readerPos {
		t.Fatalf("%s: reader position mismatch: reference %d, fast %d", name, ref.readerPos, got.readerPos)
	}
	if ref.b != got.b || ref.nb != got.nb {
		t.Fatalf("%s: bit buffer mismatch: reference b=%#x nb=%d, fast b=%#x nb=%d", name, ref.b, ref.nb, got.b, got.nb)
	}
}

func deflateLevel(t *testing.T, plain []byte, level int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, level)
	if err != nil {
		t.Fatalf("NewWriter(level %d): %v", level, err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func diffTestPlaintexts() map[string][]byte {
	rng := rand.New(rand.NewSource(1))
	random := make([]byte, 8192)
	rng.Read(random)

	// > 2x the 32KiB window with matches, forcing several readFlush pauses
	// and stateDict/stateInit resumption.
	large := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 2000)

	return map[string][]byte{
		"empty":    nil,
		"single":   []byte("a"),
		"text":     []byte(strings.Repeat("hello, world! ", 40)),
		"matches":  bytes.Repeat([]byte("abcabcabcabc0123456789"), 64),
		"random":   random,
		"large":    large,
		"mixed":    append(append([]byte{}, random[:4096]...), large[:65536]...),
		"allzero":  make([]byte, 40000),
		"alphabet": []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"),
	}
}

// TestHuffmanBytesReaderFastMatchesReference decodes identical streams with
// huffmanBytesReader and huffmanBytesReaderFast and requires identical
// output, flush boundaries, errors, roffset, reader position, and bit buffer.
func TestHuffmanBytesReaderFastMatchesReference(t *testing.T) {
	levels := []int{HuffmanOnly, NoCompression, BestSpeed, DefaultCompression, BestCompression}

	for name, plain := range diffTestPlaintexts() {
		for _, level := range levels {
			data := deflateLevel(t, plain, level)
			caseName := fmt.Sprintf("%s/level%d", name, level)
			requireEqualDecode(t, caseName, data, 0)

			// Same stream starting from a non-zero bytes.Reader offset.
			prefixed := append([]byte("JUNKPREFIX"), data...)
			requireEqualDecode(t, caseName+"/prefixed", prefixed, len("JUNKPREFIX"))
		}
	}
}

// TestHuffmanBytesReaderFastTruncated checks that both decoders agree on
// every truncation of valid streams, covering all mid-symbol EOF paths.
func TestHuffmanBytesReaderFastTruncated(t *testing.T) {
	plains := map[string][]byte{
		"text":    []byte(strings.Repeat("hello, world! ", 40)),
		"matches": bytes.Repeat([]byte("abcabcabcabc0123456789"), 64),
	}
	for name, plain := range plains {
		for _, level := range []int{HuffmanOnly, BestSpeed, BestCompression} {
			data := deflateLevel(t, plain, level)
			for cut := 0; cut < len(data); cut++ {
				requireEqualDecode(t, fmt.Sprintf("%s/level%d/cut%d", name, level, cut), data[:cut], 0)
			}
		}
	}
}

// TestHuffmanBytesReaderFastCorrupted checks that both decoders agree on
// streams with each byte corrupted in turn, covering CorruptInputError paths.
func TestHuffmanBytesReaderFastCorrupted(t *testing.T) {
	plain := bytes.Repeat([]byte("abcabcabcabc0123456789"), 64)
	for _, level := range []int{HuffmanOnly, BestSpeed, BestCompression} {
		data := deflateLevel(t, plain, level)
		for i := 0; i < len(data); i++ {
			corrupted := append([]byte{}, data...)
			corrupted[i] ^= 0xFF
			requireEqualDecode(t, fmt.Sprintf("level%d/xor%d", level, i), corrupted, 0)
		}
	}
}

// TestHuffmanBytesReaderFastGarbage checks agreement on random input that is
// not a valid deflate stream at all.
func TestHuffmanBytesReaderFastGarbage(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 500; i++ {
		data := make([]byte, rng.Intn(4096))
		rng.Read(data)
		requireEqualDecode(t, fmt.Sprintf("garbage%d", i), data, 0)
	}
}

// deflateBitWriter builds raw deflate streams bit-by-bit for crafting
// dynamic-Huffman edge cases the package writer never emits.
type deflateBitWriter struct {
	buf  []byte
	bits uint32
	n    uint
}

// writeBits writes v LSB-first, as used for header fields and extra bits.
func (w *deflateBitWriter) writeBits(v uint32, n uint) {
	w.bits |= v << w.n
	w.n += n
	for w.n >= 8 {
		w.buf = append(w.buf, byte(w.bits))
		w.bits >>= 8
		w.n -= 8
	}
}

// writeCode writes a canonical Huffman code MSB-first per RFC 1951.
func (w *deflateBitWriter) writeCode(code uint32, n uint) {
	for i := int(n) - 1; i >= 0; i-- {
		w.writeBits((code>>uint(i))&1, 1)
	}
}

func (w *deflateBitWriter) bytes() []byte {
	if w.n > 0 {
		w.buf = append(w.buf, byte(w.bits))
		w.bits, w.n = 0, 0
	}
	return w.buf
}

// canonicalCodes assigns RFC 1951 canonical codes for the given code lengths.
func canonicalCodes(lengths []int) []uint32 {
	var blCount [16]int
	for _, l := range lengths {
		if l > 0 {
			blCount[l]++
		}
	}
	var nextCode [16]uint32
	code := uint32(0)
	for l := 1; l <= 15; l++ {
		code = (code + uint32(blCount[l-1])) << 1
		nextCode[l] = code
	}
	codes := make([]uint32, len(lengths))
	for sym, l := range lengths {
		if l > 0 {
			codes[sym] = nextCode[l]
			nextCode[l]++
		}
	}
	return codes
}

var clWriteOrder = [19]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

// writeDynamicHeader writes BFINAL=1, BTYPE=dynamic, and the Huffman tables,
// returning the canonical literal and distance codes.
func writeDynamicHeader(w *deflateBitWriter, litLens, distLens, clLens []int) (litCodes, distCodes []uint32) {
	clCodes := canonicalCodes(clLens)
	w.writeBits(1, 1) // BFINAL
	w.writeBits(2, 2) // BTYPE = dynamic Huffman
	w.writeBits(uint32(len(litLens)-257), 5)
	w.writeBits(uint32(len(distLens)-1), 5)
	w.writeBits(19-4, 4)
	for _, sym := range clWriteOrder {
		w.writeBits(uint32(clLens[sym]), 3)
	}
	for _, l := range litLens {
		w.writeCode(clCodes[l], uint(clLens[l]))
	}
	for _, l := range distLens {
		w.writeCode(clCodes[l], uint(clLens[l]))
	}
	return canonicalCodes(litLens), canonicalCodes(distLens)
}

// craftedLitLens is a complete literal tree over symbols 0..257: 254 8-bit
// codes and four 9-bit codes (254/256 + 4/512 = 1), so symbol 257 (length 3)
// is available for matches.
func craftedLitLens() []int {
	litLens := make([]int, 258)
	for i := 0; i < 254; i++ {
		litLens[i] = 8
	}
	for i := 254; i < 258; i++ {
		litLens[i] = 9
	}
	return litLens
}

// craftLongDistStream builds a valid dynamic block whose distance tree has
// 15-bit codes. Decoding it forces the hd link-table lookup and the hd
// bit-refill loop, which writer-produced streams never reach.
func craftLongDistStream() (stream, expect []byte) {
	litLens := craftedLitLens()
	// Complete distance tree: lengths 1..14 once each plus 15 twice.
	distLens := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 15}
	// Complete code-length tree: all 16 symbols 0..15 at 4 bits.
	clLens := make([]int, 19)
	for i := 0; i <= 15; i++ {
		clLens[i] = 4
	}

	w := &deflateBitWriter{}
	litCodes, distCodes := writeDynamicHeader(w, litLens, distLens, clLens)

	emitLit := func(b byte) {
		w.writeCode(litCodes[b], uint(litLens[b]))
		expect = append(expect, b)
	}
	// emitMatch writes length code 257 (length 3, no extra bits) and the
	// given distance symbol.
	emitMatch := func(distSym int, extra uint32, extraBits uint, dist int) {
		w.writeCode(litCodes[257], uint(litLens[257]))
		w.writeCode(distCodes[distSym], uint(distLens[distSym]))
		if extraBits > 0 {
			w.writeBits(extra, extraBits)
		}
		for i := 0; i < 3; i++ {
			expect = append(expect, expect[len(expect)-dist])
		}
	}

	for i := 0; i < 256; i++ {
		emitLit('a')
	}
	for i := 0; i < 8; i++ {
		emitLit(byte('b' + i))
		// Distance symbol 15: 15-bit code, 6 extra bits, dist 129+64+0=193.
		emitMatch(15, 0, 6, 193)
		// Distance symbol 3: 4-bit code, dist 4.
		emitMatch(3, 0, 0, 4)
	}
	w.writeCode(litCodes[256], uint(litLens[256])) // end of block
	return w.bytes(), expect
}

// craftDegenerateLitStream builds a block whose literal tree is the
// degenerate single-code tree (only the end-of-block symbol) followed by the
// one bit pattern that hits an empty chunk, forcing the hl n==0 corrupt path.
func craftDegenerateLitStream() []byte {
	litLens := make([]int, 257)
	litLens[256] = 1
	distLens := []int{1}
	clLens := make([]int, 19)
	clLens[0], clLens[1] = 1, 1

	w := &deflateBitWriter{}
	writeDynamicHeader(w, litLens, distLens, clLens)
	w.writeBits(1, 1) // the code space only assigns bit 0
	return w.bytes()
}

// craftDegenerateDistStream builds a block with a valid literal tree but a
// degenerate single-code distance tree, then references the unassigned
// distance bit pattern, forcing the hd n==0 corrupt path.
func craftDegenerateDistStream() []byte {
	litLens := craftedLitLens()
	distLens := []int{1}
	// Code-length tree over the used lengths {1, 8, 9}.
	clLens := make([]int, 19)
	clLens[1], clLens[8], clLens[9] = 1, 2, 2

	w := &deflateBitWriter{}
	litCodes, _ := writeDynamicHeader(w, litLens, distLens, clLens)
	for _, b := range []byte("abcd") {
		w.writeCode(litCodes[b], uint(litLens[b]))
	}
	w.writeCode(litCodes[257], uint(litLens[257])) // length 3
	w.writeBits(1, 1)                              // the code space only assigns bit 0
	return w.bytes()
}

// craftDistTooFarStream builds a dynamic block whose first symbol is a match
// with distance 4 into empty history, forcing the dist > histSize corrupt
// path.
func craftDistTooFarStream() []byte {
	litLens := craftedLitLens()
	distLens := []int{1, 2, 3, 3} // complete: 1/2 + 1/4 + 2/8
	// Code-length tree over the used lengths {1, 2, 3, 8, 9}.
	// Kraft: 3 codes of length 2 (1, 8, 9) plus 2 of length 3 (2, 3) = 1.
	clLens := make([]int, 19)
	clLens[1], clLens[8], clLens[9] = 2, 2, 2
	clLens[2], clLens[3] = 3, 3

	w := &deflateBitWriter{}
	litCodes, distCodes := writeDynamicHeader(w, litLens, distLens, clLens)
	w.writeCode(litCodes[257], uint(litLens[257])) // length 3 match first
	w.writeCode(distCodes[3], uint(distLens[3]))   // dist symbol 3 -> dist 4
	return w.bytes()
}

// craftFixedDistTooBigStream builds a fixed-Huffman block using 5-bit
// distance code 30, which is outside the valid 0..29 range, forcing the
// dist >= maxNumDist corrupt path (unreachable from dynamic blocks).
func craftFixedDistTooBigStream() []byte {
	w := &deflateBitWriter{}
	w.writeBits(1, 1)              // BFINAL
	w.writeBits(1, 2)              // BTYPE = fixed Huffman
	w.writeCode(48+uint32('a'), 8) // literal 'a'
	w.writeCode(1, 7)              // length code 257 (length 3)
	w.writeCode(30, 5)             // distance code 30 >= maxNumDist
	return w.bytes()
}

// TestHuffmanBytesReaderFastCrafted covers decoder paths the package writer
// never produces: long distance codes (hd link tables and refill), degenerate
// single-code trees hitting empty chunks (n==0), invalid distances, and their
// truncations.
func TestHuffmanBytesReaderFastCrafted(t *testing.T) {
	longDist, expect := craftLongDistStream()

	// The crafted valid stream must decode successfully to the simulated
	// LZ77 output; anything else means the crafting itself is wrong.
	ref := decodeBytesReader(longDist, 0, false)
	if ref.err != io.EOF {
		t.Fatalf("longdist reference decode err = %v, want io.EOF", ref.err)
	}
	if !bytes.Equal(ref.output, expect) {
		t.Fatalf("longdist reference output mismatch: got %d bytes, want %d", len(ref.output), len(expect))
	}

	corrupt := map[string][]byte{
		"degenerate-lit":     craftDegenerateLitStream(),
		"degenerate-dist":    craftDegenerateDistStream(),
		"dist-too-far":       craftDistTooFarStream(),
		"fixed-dist-too-big": craftFixedDistTooBigStream(),
	}
	for name, data := range corrupt {
		res := decodeBytesReader(data, 0, false)
		if _, ok := res.err.(CorruptInputError); !ok {
			t.Fatalf("%s reference decode err = %v, want CorruptInputError", name, res.err)
		}
	}

	requireEqualDecode(t, "longdist", longDist, 0)
	for name, data := range corrupt {
		requireEqualDecode(t, name, data, 0)
	}

	for cut := 0; cut < len(longDist); cut++ {
		requireEqualDecode(t, fmt.Sprintf("longdist/cut%d", cut), longDist[:cut], 0)
	}
}
