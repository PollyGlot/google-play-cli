package artifacttest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func Zip(t *testing.T, dir, name string, members map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range members {
		hdr := &zip.FileHeader{Name: n, Method: zip.Deflate}
		if len(b) == 0 {
			hdr.Method = zip.Store
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", n, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("write %q: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return WriteFile(t, dir, name, buf.Bytes())
}

func WriteFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func AAB(t *testing.T, dir, name, pkg string) string {
	t.Helper()
	return Zip(t, dir, name, map[string][]byte{
		"BundleConfig.pb":                   {},
		"base/manifest/AndroidManifest.xml": ProtoManifest(pkg),
		"base/dex/classes.dex":              []byte("not a real dex"),
	})
}

func APK(t *testing.T, dir, name, pkg string) string {
	t.Helper()
	return Zip(t, dir, name, map[string][]byte{
		"AndroidManifest.xml": BinaryXMLManifest(pkg, false),
		"classes.dex":         []byte("not a real dex"),
	})
}

func FillerMembers(n int) map[string][]byte {
	m := make(map[string][]byte, n)
	for i := range n {
		m["res/raw/filler"+strconv.Itoa(i)] = nil
	}
	return m
}

func AABWith(t *testing.T, dir, name, pkg string, extra map[string][]byte) string {
	t.Helper()
	return Zip(t, dir, name, merge(extra, map[string][]byte{
		"BundleConfig.pb":                   {},
		"base/manifest/AndroidManifest.xml": ProtoManifest(pkg),
	}))
}

func APKWith(t *testing.T, dir, name, pkg string, extra map[string][]byte) string {
	t.Helper()
	return Zip(t, dir, name, merge(extra, map[string][]byte{
		"AndroidManifest.xml": BinaryXMLManifest(pkg, false),
	}))
}

func merge(base, over map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func ProtoManifest(pkg string) []byte {
	attr := concat(
		field(2, []byte("package")),
		field(3, []byte(pkg)),
	)
	element := concat(
		field(3, []byte("manifest")),
		field(4, attr),
	)
	return field(1, element)
}

func field(num int, payload []byte) []byte {
	var out []byte
	out = binary.AppendUvarint(out, uint64(num)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

const (
	xmlChunkType        = 0x0003
	stringPoolChunkType = 0x0001
	startElemChunkType  = 0x0102
	utf8PoolFlag        = 1 << 8
	noEntry             = 0xFFFFFFFF
	resTypeString       = 0x03
)

func BinaryXMLManifest(pkg string, utf16Pool bool) []byte {
	return binaryXMLDocument(stringPool([]string{"manifest", "package", pkg}, utf16Pool, 0))
}

func AliasedPoolManifest(pkg string, utf16Pool bool, fillerLen, aliases int) []byte {
	filler := strings.Repeat("x", fillerLen)
	return binaryXMLDocument(stringPool([]string{"manifest", "package", pkg, filler}, utf16Pool, aliases))
}

func RepeatedPoolManifest(pkg string, pools, fillerLen, aliases int) []byte {
	filler := stringPool([]string{strings.Repeat("x", fillerLen)}, false, aliases)
	chunks := make([][]byte, 0, pools+1)
	for range pools {
		chunks = append(chunks, filler)
	}
	return binaryXMLDocument(append(chunks, stringPool([]string{"manifest", "package", pkg}, false, 0))...)
}

const (
	poolManifest = 0
	poolPackage  = 1
	poolPkg      = 2
)

func binaryXMLDocument(pools ...[]byte) []byte {
	elem := startElement(poolManifest, poolPackage, poolPkg)

	var body bytes.Buffer
	for _, p := range pools {
		body.Write(p)
	}
	body.Write(elem)

	var out bytes.Buffer
	writeU16(&out, xmlChunkType)
	writeU16(&out, 8)
	writeU32(&out, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func startElement(nameIdx, attrNameIdx, attrValueIdx uint32) []byte {
	var out bytes.Buffer
	const size = 16 + 20 + 20
	writeU16(&out, startElemChunkType)
	writeU16(&out, 16)
	writeU32(&out, size)
	writeU32(&out, 1)
	writeU32(&out, noEntry)
	writeU32(&out, noEntry)
	writeU32(&out, nameIdx)
	writeU16(&out, 20)
	writeU16(&out, 20)
	writeU16(&out, 1)
	writeU16(&out, 0)
	writeU16(&out, 0)
	writeU16(&out, 0)
	writeU32(&out, noEntry)
	writeU32(&out, attrNameIdx)
	writeU32(&out, attrValueIdx)
	writeU16(&out, 8)
	out.WriteByte(0)
	out.WriteByte(resTypeString)
	writeU32(&out, attrValueIdx)
	return out.Bytes()
}

func stringPool(strs []string, utf16Pool bool, aliases int) []byte {
	var (
		data    bytes.Buffer
		offsets = make([]uint32, len(strs))
	)
	for i, s := range strs {
		offsets[i] = uint32(data.Len())
		if utf16Pool {
			units := utf16Units(s)
			writeU16(&data, uint16(len(units)))
			for _, u := range units {
				writeU16(&data, u)
			}
			writeU16(&data, 0)
			continue
		}
		writeLen8(&data, len([]rune(s)))
		writeLen8(&data, len(s))
		data.WriteString(s)
		data.WriteByte(0)
	}
	for range aliases {
		offsets = append(offsets, offsets[len(offsets)-1])
	}
	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}

	const headerSize = 28
	stringsStart := headerSize + 4*len(offsets)

	var out bytes.Buffer
	writeU16(&out, stringPoolChunkType)
	writeU16(&out, headerSize)
	writeU32(&out, uint32(stringsStart+data.Len()))
	writeU32(&out, uint32(len(offsets)))
	writeU32(&out, 0)
	if utf16Pool {
		writeU32(&out, 0)
	} else {
		writeU32(&out, utf8PoolFlag)
	}
	writeU32(&out, uint32(stringsStart))
	writeU32(&out, 0)
	for _, o := range offsets {
		writeU32(&out, o)
	}
	out.Write(data.Bytes())
	return out.Bytes()
}

func writeLen8(b *bytes.Buffer, n int) {
	if n > 0x7F {
		b.WriteByte(byte(0x80 | n>>8))
	}
	b.WriteByte(byte(n))
}

func utf16Units(s string) []uint16 {
	var units []uint16
	for _, r := range s {
		if r < 0x10000 {
			units = append(units, uint16(r))
			continue
		}
		r -= 0x10000
		units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
	}
	return units
}

func writeU16(b *bytes.Buffer, v uint16) {
	_ = binary.Write(b, binary.LittleEndian, v)
}

func writeU32(b *bytes.Buffer, v uint32) {
	_ = binary.Write(b, binary.LittleEndian, v)
}
