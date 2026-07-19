//go:build generate
// +build generate

//go:generate go run $GOFILE
//go:generate go fmt ../inflate_gen.go
//go:generate go fmt ../huffman_bytes_reader_fast.go

package main

import (
	"os"
	"strings"
)

// body is the Huffman block decoder shared by every reader variant, including
// the bytes.Reader fast path. Markers:
//
//	$FUNCNAME$           decoder method name
//	$TYPE$               concrete reader type asserted from f.r
//	$PREAMBLE$           reader-specific locals
//	$HLSYMCOMMENT$       comment above the hl huffSym maxRead read
//	$HDSYM$              comments and maxRead read for the hd huffSym loop
//	$READBYTE|kind$      load one byte into the bit buffer; kind selects the
//	$READBYTE|kind|msg$  EOF error (noeof or raw) and msg an optional
//	                     debugDecode message
//	$ROFFSET$            the input-offset variable
//	$FLUSHSTEPINIT$      step assignment for the literal flush pause
//	$FLUSHSTEPDICT$      step assignment for the copy flush pause
//	$RETURN$             exit statement
//	$TRAILER$            code between the final goto and the closing brace
const body = `func (f *decompressor) $FUNCNAME$() {
	const (
		stateInit = iota // Zero value must be stateInit
		stateDict
	)
$PREAMBLE$

	switch f.stepState {
	case stateInit:
		goto readLiteral
	case stateDict:
		goto copyHistory
	}

readLiteral:
	// Read literal and/or (length, distance) according to RFC section 3.2.3.
	{
		var v int
		{
			// Inlined v, err := f.huffSym(f.hl)
$HLSYMCOMMENT$
			n := uint(f.hl.maxRead)
			for {
				for fnb < n {
					$READBYTE|noeof$
				}
				chunk := f.hl.chunks[fb&(huffmanNumChunks-1)]
				n = uint(chunk & huffmanCountMask)
				if n > huffmanChunkBits {
					chunk = f.hl.links[chunk>>huffmanValueShift][(fb>>huffmanChunkBits)&f.hl.linkMask]
					n = uint(chunk & huffmanCountMask)
				}
				if n <= fnb {
					if n == 0 {
						f.b, f.nb = fb, fnb
						if debugDecode {
							fmt.Println("huffsym: n==0")
						}
						f.err = CorruptInputError($ROFFSET$)
						$RETURN$
					}
					fb = fb >> (n & regSizeMaskUint32)
					fnb = fnb - n
					v = int(chunk >> huffmanValueShift)
					break
				}
			}
		}

		var length int
		switch {
		case v < 256:
			dict.writeByte(byte(v))
			if dict.availWrite() == 0 {
				f.toRead = dict.readFlush()
$FLUSHSTEPINIT$
				f.stepState = stateInit
				f.b, f.nb = fb, fnb
				$RETURN$
			}
			goto readLiteral
		case v == 256:
			f.b, f.nb = fb, fnb
			f.finishBlock()
			$RETURN$
		// otherwise, reference to older data
		case v < 265:
			length = v - (257 - 3)
		case v < maxNumLit:
			val := decCodeToLen[(v - 257)]
			length = int(val.length) + 3
			n := uint(val.extra)
			for fnb < n {
				$READBYTE|raw|morebits n>0:$
			}
			length += int(fb & bitMask32[n])
			fb >>= n & regSizeMaskUint32
			fnb -= n
		default:
			if debugDecode {
				fmt.Println(v, ">= maxNumLit")
			}
			f.err = CorruptInputError($ROFFSET$)
			f.b, f.nb = fb, fnb
			$RETURN$
		}

		var dist uint32
		if f.hd == nil {
			for fnb < 5 {
				$READBYTE|raw|morebits f.nb<5:$
			}
			dist = uint32(bits.Reverse8(uint8(fb & 0x1F << 3)))
			fb >>= 5
			fnb -= 5
		} else {
$HDSYM$
			for {
				for fnb < n {
					$READBYTE|noeof$
				}
				chunk := f.hd.chunks[fb&(huffmanNumChunks-1)]
				n = uint(chunk & huffmanCountMask)
				if n > huffmanChunkBits {
					chunk = f.hd.links[chunk>>huffmanValueShift][(fb>>huffmanChunkBits)&f.hd.linkMask]
					n = uint(chunk & huffmanCountMask)
				}
				if n <= fnb {
					if n == 0 {
						f.b, f.nb = fb, fnb
						if debugDecode {
							fmt.Println("huffsym: n==0")
						}
						f.err = CorruptInputError($ROFFSET$)
						$RETURN$
					}
					fb = fb >> (n & regSizeMaskUint32)
					fnb = fnb - n
					dist = uint32(chunk >> huffmanValueShift)
					break
				}
			}
		}

		switch {
		case dist < 4:
			dist++
		case dist < maxNumDist:
			nb := uint(dist-2) >> 1
			// have 1 bit in bottom of dist, need nb more.
			extra := (dist & 1) << (nb & regSizeMaskUint32)
			for fnb < nb {
				$READBYTE|raw|morebits f.nb<nb:$
			}
			extra |= fb & bitMask32[nb]
			fb >>= nb & regSizeMaskUint32
			fnb -= nb
			dist = 1<<((nb+1)&regSizeMaskUint32) + 1 + extra
			// slower: dist = bitMask32[nb+1] + 2 + extra
		default:
			f.b, f.nb = fb, fnb
			if debugDecode {
				fmt.Println("dist too big:", dist, maxNumDist)
			}
			f.err = CorruptInputError($ROFFSET$)
			$RETURN$
		}

		// No check on length; encoding can be prescient.
		if dist > uint32(dict.histSize()) {
			f.b, f.nb = fb, fnb
			if debugDecode {
				fmt.Println("dist > dict.histSize():", dist, dict.histSize())
			}
			f.err = CorruptInputError($ROFFSET$)
			$RETURN$
		}

		f.copyLen, f.copyDist = length, int(dist)
		goto copyHistory
	}

copyHistory:
	// Perform a backwards copy according to RFC section 3.2.3.
	{
		cnt := dict.tryWriteCopy(f.copyDist, f.copyLen)
		if cnt == 0 {
			cnt = dict.writeCopy(f.copyDist, f.copyLen)
		}
		f.copyLen -= cnt

		if dict.availWrite() == 0 || f.copyLen > 0 {
			f.toRead = dict.readFlush()
$FLUSHSTEPDICT$
			f.stepState = stateDict
			f.b, f.nb = fb, fnb
			$RETURN$
		}
		goto readLiteral
	}
$TRAILER$
`

const slowDoc = `// Decode a single Huffman block from f.
// hl and hd are the Huffman states for the lit/length values
// and the distance values, respectively. If hd == nil, using the
// fixed distance encoding associated with fixed Huffman blocks.
`

const fastDoc = "// huffmanBytesReaderFast decodes one Huffman block from `*bytes.Reader` without\n" +
	"// per-byte `ReadByte` calls. It snapshots the backing slice and byte index,\n" +
	"// advances a local index while decoding, then writes the index back before\n" +
	"// return and clears `prevRune` after any byte read.\n"

const slowPreamble = `	fr := f.r.($TYPE$)

	// Optimization. Compiler isn't smart enough to keep f.b,f.nb in registers,
	// but is smart enough to keep local variables in registers, so use nb and b,
	// inline call to moreBits and reassign b,nb back to f on return.
	fnb, fb, dict := f.nb, f.b, &f.dict`

const fastPreamble = `	fr := f.r.(*bytes.Reader)
	frState := bytesReaderStateOf(fr)
	frBuf := frState.s
	frPos := frState.i
	frRead := false
	roffset := f.roffset

	// Keep the bit buffer and dictionary in locals for the hot loop. The
	// decoder writes b and nb back to f before each return.
	fnb, fb, dict := f.nb, f.b, &f.dict`

const slowHLSymComment = `			// Since a huffmanDecoder can be empty or be composed of a degenerate tree
			// with single element, huffSym must error on these two edge cases. In both
			// cases, the chunks slice will be 0 for the invalid sequence, leading it
			// satisfy the n == 0 check below.`

const fastHLSymComment = `			// A chunk count of zero marks an invalid Huffman sequence.`

const slowHDSym = `			// Since a huffmanDecoder can be empty or be composed of a degenerate tree
			// with single element, huffSym must error on these two edge cases. In both
			// cases, the chunks slice will be 0 for the invalid sequence, leading it
			// satisfy the n == 0 check below.
			n := uint(f.hd.maxRead)
			// Optimization. Compiler isn't smart enough to keep f.b,f.nb in registers,
			// but is smart enough to keep local variables in registers, so use nb and b,
			// inline call to moreBits and reassign b,nb back to f on return.`

const fastHDSym = `			// A chunk count of zero marks an invalid Huffman sequence.
			n := uint(f.hd.maxRead)`

const slowFlushStepInit = `				f.step = $FUNCNAME$`

const fastFlushStepInit = "\t\t\t\t// huffmanBytesReader is the shared resume step for\n" +
	"\t\t\t\t// `*bytes.Reader` blocks; doStep decides which decoder\n" +
	"\t\t\t\t// handles it. Both decoders share the same suspended\n" +
	"\t\t\t\t// state (b/nb/roffset/dict/reader index), so either can\n" +
	"\t\t\t\t// resume this block.\n" +
	"\t\t\t\tf.step = huffmanBytesReader"

const slowFlushStepDict = `			f.step = $FUNCNAME$ // We need to continue this work`

const fastFlushStepDict = "\t\t\t// See the readLiteral flush above: shared resume step for\n" +
	"\t\t\t// `*bytes.Reader` blocks, routed by doStep.\n" +
	"\t\t\tf.step = huffmanBytesReader // We need to continue this work"

const slowTrailer = `	// Not reached
}`

const fastTrailer = "\nsaveReturn:\n" +
	"\tf.roffset = roffset\n" +
	"\tfrState.i = frPos\n" +
	"\tif frRead {\n" +
	"\t\t// Match `bytes.Reader.ReadByte`, which invalidates `UnreadRune`\n" +
	"\t\t// before checking for EOF: any read attempt, successful or not,\n" +
	"\t\t// clears `prevRune`.\n" +
	"\t\tfrState.prevRune = -1\n" +
	"\t}\n" +
	"}"

// render instantiates the shared body for one decoder variant.
func render(fast bool, funcName, typ string) string {
	s := body
	if fast {
		s = strings.ReplaceAll(s, "$PREAMBLE$", fastPreamble)
		s = strings.ReplaceAll(s, "$HLSYMCOMMENT$", fastHLSymComment)
		s = strings.ReplaceAll(s, "$HDSYM$", fastHDSym)
		s = strings.ReplaceAll(s, "$FLUSHSTEPINIT$", fastFlushStepInit)
		s = strings.ReplaceAll(s, "$FLUSHSTEPDICT$", fastFlushStepDict)
		s = strings.ReplaceAll(s, "$ROFFSET$", "roffset")
		s = strings.ReplaceAll(s, "$RETURN$", "goto saveReturn")
		s = strings.ReplaceAll(s, "$TRAILER$", fastTrailer)
	} else {
		s = strings.ReplaceAll(s, "$PREAMBLE$", slowPreamble)
		s = strings.ReplaceAll(s, "$HLSYMCOMMENT$", slowHLSymComment)
		s = strings.ReplaceAll(s, "$HDSYM$", slowHDSym)
		s = strings.ReplaceAll(s, "$FLUSHSTEPINIT$", slowFlushStepInit)
		s = strings.ReplaceAll(s, "$FLUSHSTEPDICT$", slowFlushStepDict)
		s = strings.ReplaceAll(s, "$ROFFSET$", "f.roffset")
		s = strings.ReplaceAll(s, "$RETURN$", "return")
		s = strings.ReplaceAll(s, "$TRAILER$", slowTrailer)
	}
	s = strings.ReplaceAll(s, "$FUNCNAME$", funcName)
	s = strings.ReplaceAll(s, "$TYPE$", typ)
	return expandReadBytes(s, fast)
}

// expandReadBytes replaces each $READBYTE|kind$ or $READBYTE|kind|msg$ marker
// line with the byte-load sequence for the variant, preserving the marker's
// indentation.
func expandReadBytes(s string, fast bool) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, "\t")
		if !strings.HasPrefix(trimmed, "$READBYTE") {
			out = append(out, line)
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		spec := strings.TrimSuffix(strings.TrimPrefix(trimmed, "$READBYTE|"), "$")
		kind, msg, _ := strings.Cut(spec, "|")
		out = append(out, renderReadByte(indent, kind, msg, fast)...)
	}
	return strings.Join(out, "\n")
}

func renderReadByte(indent, kind, msg string, fast bool) []string {
	at := func(depth int, text string) string {
		return indent + strings.Repeat("\t", depth) + text
	}
	var l []string
	if fast {
		errVal := "io.ErrUnexpectedEOF"
		if kind == "raw" {
			errVal = "io.EOF"
		}
		l = append(l,
			at(0, "if frPos >= int64(len(frBuf)) {"),
			at(1, "frRead = true"),
			at(1, "f.b, f.nb = fb, fnb"),
		)
		if msg != "" {
			l = append(l,
				at(1, "if debugDecode {"),
				at(2, `fmt.Println("`+msg+`", io.EOF)`),
				at(1, "}"),
			)
		}
		l = append(l,
			at(1, "f.err = "+errVal),
			at(1, "goto saveReturn"),
			at(0, "}"),
			at(0, "c := frBuf[frPos]"),
			at(0, "frPos++"),
			at(0, "frRead = true"),
			at(0, "roffset++"),
		)
	} else {
		errExpr := "noEOF(err)"
		if kind == "raw" {
			errExpr = "err"
		}
		l = append(l,
			at(0, "c, err := fr.ReadByte()"),
			at(0, "if err != nil {"),
			at(1, "f.b, f.nb = fb, fnb"),
		)
		if msg != "" {
			l = append(l,
				at(1, "if debugDecode {"),
				at(2, `fmt.Println("`+msg+`", err)`),
				at(1, "}"),
			)
		}
		l = append(l,
			at(1, "f.err = "+errExpr),
			at(1, "return"),
			at(0, "}"),
			at(0, "f.roffset++"),
		)
	}
	l = append(l,
		at(0, "fb |= uint32(c) << (fnb & regSizeMaskUint32)"),
		at(0, "fnb += 8"),
	)
	return l
}

func writeFile(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func main() {
	types := []string{"*bytes.Buffer", "*bytes.Reader", "*bufio.Reader", "*strings.Reader", "Reader"}
	names := []string{"BytesBuffer", "BytesReader", "BufioReader", "StringsReader", "GenericReader"}

	var gen strings.Builder
	gen.WriteString(`// Code generated by go generate gen_inflate.go. DO NOT EDIT.

package flate

import (
	"bufio"
	"bytes"
	"fmt"
	"math/bits"
	"strings"
)

`)
	for i, t := range types {
		gen.WriteString(slowDoc)
		gen.WriteString(render(false, "huffman"+names[i], t))
		gen.WriteString("\n\n")
	}
	gen.WriteString("func (f *decompressor) huffmanBlockDecoder() {\n")
	gen.WriteString("\tswitch f.r.(type) {\n")
	for i, t := range types {
		gen.WriteString("\tcase " + t + ":\n")
		gen.WriteString("\t\tf.huffman" + names[i] + "()\n")
	}
	gen.WriteString("\tdefault:\n")
	gen.WriteString("\t\tf.huffmanGenericReader()\n")
	gen.WriteString("\t}\n}\n")
	writeFile("../inflate_gen.go", gen.String())

	var fastGen strings.Builder
	fastGen.WriteString(`// Code generated by go generate gen_inflate.go. DO NOT EDIT.

package flate

import (
	"bytes"
	"fmt"
	"io"
	"math/bits"
)

`)
	fastGen.WriteString(fastDoc)
	fastGen.WriteString(render(true, "huffmanBytesReaderFast", "*bytes.Reader"))
	fastGen.WriteString("\n")
	writeFile("../huffman_bytes_reader_fast.go", fastGen.String())
}
