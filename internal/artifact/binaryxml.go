package artifact

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// This file implements the minimum of Android's binary XML (the compiled
// AndroidManifest.xml inside an APK) needed to answer one question: what
// package does this artifact declare? It is a reader, never a writer, and it
// walks only as far as the first <manifest> start-element.
//
// The format is AOSP's ResChunk_header tree (frameworks/base ResourceTypes.h)
// and it has been stable since Android 1.0. The alternative was linking
// aapt2 or an androidpublisher client, which ADR-0007 rules out; a hundred
// lines of bounds-checked reader keeps the binary a single static build.
//
// Every offset read here comes from a file gplay did not produce, so the
// reader's only invariant is that it never panics and never reads out of
// bounds: every failure is an error the caller degrades on (a parser gap
// must not become a false refusal).

// Chunk types from ResourceTypes.h; only the three the walk needs.
const (
	chunkTypeStringPool   = 0x0001
	chunkTypeXML          = 0x0003
	chunkTypeXMLStartElem = 0x0102
)

// utf8Flag marks a string pool whose entries are modified-UTF-8 rather than
// UTF-16LE. aapt2 emits UTF-8 pools for manifests; older aapt emitted UTF-16.
const utf8Flag = 1 << 8

// noEntry is the sentinel Android uses for "no such string / no namespace".
const noEntry = 0xFFFFFFFF

// typeString is Res_value::TYPE_STRING: the data word is a string-pool index.
const typeString = 0x03

var errShortChunk = errors.New("truncated chunk")

// parseBinaryXMLPackage returns the `package` attribute of the root
// <manifest> element of a compiled AndroidManifest.xml.
func parseBinaryXMLPackage(b []byte) (string, error) {
	if len(b) < 8 {
		return "", errShortChunk
	}
	if binary.LittleEndian.Uint16(b[0:2]) != chunkTypeXML {
		return "", fmt.Errorf("not a binary XML document (chunk type 0x%04x)", binary.LittleEndian.Uint16(b[0:2]))
	}
	headerSize := int(binary.LittleEndian.Uint16(b[2:4]))
	total := int(binary.LittleEndian.Uint32(b[4:8]))
	if headerSize < 8 || headerSize > len(b) {
		return "", errShortChunk
	}
	// A truncated tail is tolerated by clamping to what we actually have:
	// the manifest element sits near the front, so a partial file can still
	// answer the question.
	if total > len(b) || total <= 0 {
		total = len(b)
	}

	var pool []string
	for off := headerSize; off+8 <= total; {
		ctype := int(binary.LittleEndian.Uint16(b[off : off+2]))
		csize := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		// A zero or negative chunk size would spin forever; an oversized one
		// would read past the document.
		if csize < 8 || off+csize > total {
			return "", errShortChunk
		}
		chunk := b[off : off+csize]

		switch ctype {
		case chunkTypeStringPool:
			p, err := parseStringPool(chunk)
			if err != nil {
				return "", err
			}
			pool = p
		case chunkTypeXMLStartElem:
			name, attrs, err := parseStartElement(chunk)
			if err != nil {
				return "", err
			}
			if str(pool, name) != "manifest" {
				// The first start-element of a manifest IS <manifest>;
				// anything else means this is not the document we want.
				return "", fmt.Errorf("first element is %q, not <manifest>", str(pool, name))
			}
			for _, a := range attrs {
				// package carries no namespace: an android:-prefixed
				// attribute named "package" is a different thing.
				if a.ns != noEntry || str(pool, a.name) != "package" {
					continue
				}
				if v := str(pool, a.rawValue); v != "" {
					return v, nil
				}
				if a.dataType == typeString {
					if v := str(pool, a.data); v != "" {
						return v, nil
					}
				}
				return "", errors.New("<manifest> package attribute has no string value")
			}
			return "", errors.New("<manifest> declares no package attribute")
		}
		off += csize
	}
	return "", errors.New("no <manifest> start element found")
}

// str resolves a string-pool index, returning "" for the noEntry sentinel or
// any out-of-range index rather than panicking.
func str(pool []string, idx uint32) string {
	if idx == noEntry || uint64(idx) >= uint64(len(pool)) {
		return ""
	}
	return pool[idx]
}

// xmlAttr is one attribute of a start-element, indices unresolved.
type xmlAttr struct {
	ns       uint32
	name     uint32
	rawValue uint32
	dataType uint8
	data     uint32
}

// parseStartElement reads a RES_XML_START_ELEMENT chunk: the element name
// index and its attributes.
func parseStartElement(c []byte) (name uint32, attrs []xmlAttr, err error) {
	// ResXMLTree_node is 8 (header) + 4 (lineNumber) + 4 (comment) = 16,
	// then ResXMLTree_attrExt.
	const extOff = 16
	if len(c) < extOff+20 {
		return 0, nil, errShortChunk
	}
	name = binary.LittleEndian.Uint32(c[extOff+4 : extOff+8])
	attrStart := int(binary.LittleEndian.Uint16(c[extOff+8 : extOff+10]))
	attrSize := int(binary.LittleEndian.Uint16(c[extOff+10 : extOff+12]))
	attrCount := int(binary.LittleEndian.Uint16(c[extOff+12 : extOff+14]))
	// attributeStart is relative to the start of attrExt, not to the chunk.
	base := extOff + attrStart
	// Each attribute is ns+name+rawValue (12) then a Res_value (8).
	if attrSize < 20 || base < extOff {
		return 0, nil, errShortChunk
	}
	for i := 0; i < attrCount; i++ {
		off := base + i*attrSize
		if off < 0 || off+20 > len(c) {
			return 0, nil, errShortChunk
		}
		attrs = append(attrs, xmlAttr{
			ns:       binary.LittleEndian.Uint32(c[off : off+4]),
			name:     binary.LittleEndian.Uint32(c[off+4 : off+8]),
			rawValue: binary.LittleEndian.Uint32(c[off+8 : off+12]),
			dataType: c[off+15],
			data:     binary.LittleEndian.Uint32(c[off+16 : off+20]),
		})
	}
	return name, attrs, nil
}

// parseStringPool reads a RES_STRING_POOL chunk into a flat slice. Style
// spans are ignored: nothing here needs them.
func parseStringPool(c []byte) ([]string, error) {
	const poolHeader = 28
	if len(c) < poolHeader {
		return nil, errShortChunk
	}
	headerSize := int(binary.LittleEndian.Uint16(c[2:4]))
	count := int(binary.LittleEndian.Uint32(c[8:12]))
	flags := binary.LittleEndian.Uint32(c[16:20])
	stringsStart := int(binary.LittleEndian.Uint32(c[20:24]))
	if headerSize < poolHeader || headerSize > len(c) {
		return nil, errShortChunk
	}
	// Refuse an index array that cannot fit: count is attacker-controlled and
	// would otherwise drive a huge allocation.
	if count < 0 || headerSize+count*4 > len(c) {
		return nil, errShortChunk
	}
	if stringsStart < 0 || stringsStart > len(c) {
		return nil, errShortChunk
	}
	utf8 := flags&utf8Flag != 0

	out := make([]string, count)
	for i := range count {
		off := int(binary.LittleEndian.Uint32(c[headerSize+i*4 : headerSize+i*4+4]))
		at := stringsStart + off
		if off < 0 || at < 0 || at >= len(c) {
			// A bad offset costs that one string, not the whole pool: the
			// walk only needs "manifest" and "package" to resolve.
			continue
		}
		if utf8 {
			out[i] = readUTF8String(c[at:])
		} else {
			out[i] = readUTF16String(c[at:])
		}
	}
	return out, nil
}

// readUTF8String reads aapt's UTF-8 pool entry: a varint character count, a
// varint byte count, then that many bytes.
func readUTF8String(b []byte) string {
	_, n := readLen8(b)
	if n < 0 {
		return ""
	}
	b = b[n:]
	byteLen, n := readLen8(b)
	if n < 0 {
		return ""
	}
	b = b[n:]
	if byteLen > len(b) {
		byteLen = len(b)
	}
	return string(b[:byteLen])
}

// readLen8 reads the 1-or-2-byte length prefix of a UTF-8 pool entry,
// returning the value and how many bytes it consumed (-1 when truncated).
func readLen8(b []byte) (int, int) {
	if len(b) < 1 {
		return 0, -1
	}
	if b[0]&0x80 == 0 {
		return int(b[0]), 1
	}
	if len(b) < 2 {
		return 0, -1
	}
	return int(b[0]&0x7F)<<8 | int(b[1]), 2
}

// readUTF16String reads aapt's UTF-16 pool entry: a 1-or-2-word length
// prefix, then that many UTF-16LE code units.
func readUTF16String(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	n := int(binary.LittleEndian.Uint16(b[0:2]))
	b = b[2:]
	if n&0x8000 != 0 {
		if len(b) < 2 {
			return ""
		}
		n = (n&0x7FFF)<<16 | int(binary.LittleEndian.Uint16(b[0:2]))
		b = b[2:]
	}
	if n < 0 || n*2 > len(b) {
		n = len(b) / 2
	}
	units := make([]uint16, n)
	for i := range n {
		units[i] = binary.LittleEndian.Uint16(b[i*2 : i*2+2])
	}
	return string(utf16.Decode(units))
}
