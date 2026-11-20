package artifact

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

const (
	chunkTypeStringPool   = 0x0001
	chunkTypeXML          = 0x0003
	chunkTypeXMLStartElem = 0x0102
)

const utf8Flag = 1 << 8

const noEntry = 0xFFFFFFFF

const typeString = 0x03

var errShortChunk = errors.New("truncated chunk")

const maxPoolDecodeBytes = maxManifestBytes

var errPoolBudget = fmt.Errorf("string pool decodes more than %d bytes", maxPoolDecodeBytes)

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
	if total > len(b) || total <= 0 {
		total = len(b)
	}

	var pool []string
	budget := maxPoolDecodeBytes
	for off := headerSize; off+8 <= total; {
		ctype := int(binary.LittleEndian.Uint16(b[off : off+2]))
		csize := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		if csize < 8 || off+csize > total {
			return "", errShortChunk
		}
		chunk := b[off : off+csize]

		switch ctype {
		case chunkTypeStringPool:
			p, err := parseStringPool(chunk, &budget)
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
				return "", fmt.Errorf("first element is %q, not <manifest>", str(pool, name))
			}
			for _, a := range attrs {
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

func str(pool []string, idx uint32) string {
	if idx == noEntry || uint64(idx) >= uint64(len(pool)) {
		return ""
	}
	return pool[idx]
}

type xmlAttr struct {
	ns       uint32
	name     uint32
	rawValue uint32
	dataType uint8
	data     uint32
}

func parseStartElement(c []byte) (name uint32, attrs []xmlAttr, err error) {
	const extOff = 16
	if len(c) < extOff+20 {
		return 0, nil, errShortChunk
	}
	name = binary.LittleEndian.Uint32(c[extOff+4 : extOff+8])
	attrStart := int(binary.LittleEndian.Uint16(c[extOff+8 : extOff+10]))
	attrSize := int(binary.LittleEndian.Uint16(c[extOff+10 : extOff+12]))
	attrCount := int(binary.LittleEndian.Uint16(c[extOff+12 : extOff+14]))
	base := extOff + attrStart
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

func parseStringPool(c []byte, budget *int) ([]string, error) {
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
			continue
		}
		var err error
		if utf8 {
			out[i], err = readUTF8String(c[at:], budget)
		} else {
			out[i], err = readUTF16String(c[at:], budget)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readUTF8String(b []byte, budget *int) (string, error) {
	_, n := readLen8(b)
	if n < 0 {
		return "", nil
	}
	b = b[n:]
	byteLen, n := readLen8(b)
	if n < 0 {
		return "", nil
	}
	b = b[n:]
	if byteLen > len(b) {
		byteLen = len(b)
	}
	if err := debit(budget, byteLen); err != nil {
		return "", err
	}
	return string(b[:byteLen]), nil
}

func debit(budget *int, n int) error {
	if n > *budget {
		return errPoolBudget
	}
	*budget -= n
	return nil
}

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

func readUTF16String(b []byte, budget *int) (string, error) {
	if len(b) < 2 {
		return "", nil
	}
	n := int(binary.LittleEndian.Uint16(b[0:2]))
	b = b[2:]
	if n&0x8000 != 0 {
		if len(b) < 2 {
			return "", nil
		}
		n = (n&0x7FFF)<<16 | int(binary.LittleEndian.Uint16(b[0:2]))
		b = b[2:]
	}
	if n < 0 || n*2 > len(b) {
		n = len(b) / 2
	}
	if err := debit(budget, n*2); err != nil {
		return "", err
	}
	units := make([]uint16, n)
	for i := range n {
		units[i] = binary.LittleEndian.Uint16(b[i*2 : i*2+2])
	}
	return string(utf16.Decode(units)), nil
}
