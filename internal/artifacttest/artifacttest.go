// Package artifacttest builds the tiny fixture artifacts the preflight
// suites need: real zip containers carrying real (if minimal) Android
// manifests, in the two encodings gplay parses. Building them in-test is
// what keeps the suites offline and the repo free of multi-megabyte binary
// fixtures, and it doubles as an executable spec of the two formats.
//
// Production code must not import this package.
package artifacttest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Zip writes a zip archive with the given members and returns its path.
func Zip(t *testing.T, dir, name string, members map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range members {
		w, err := zw.Create(n)
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

// WriteFile writes raw bytes to dir/name and returns the path.
func WriteFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// AAB writes a minimal Android App Bundle declaring pkg: a zip with
// BundleConfig.pb and a protobuf manifest under the base module, the two
// members gplay's classifier keys on.
func AAB(t *testing.T, dir, name, pkg string) string {
	t.Helper()
	return Zip(t, dir, name, map[string][]byte{
		"BundleConfig.pb":                   {},
		"base/manifest/AndroidManifest.xml": ProtoManifest(pkg),
		"base/dex/classes.dex":              []byte("not a real dex"),
	})
}

// APK writes a minimal APK declaring pkg: a zip with a binary-XML
// AndroidManifest.xml at its root.
func APK(t *testing.T, dir, name, pkg string) string {
	t.Helper()
	return Zip(t, dir, name, map[string][]byte{
		"AndroidManifest.xml": BinaryXMLManifest(pkg, false),
		"classes.dex":         []byte("not a real dex"),
	})
}

// ProtoManifest encodes the aapt2 protobuf XmlNode an AAB carries:
// <manifest package="pkg"/>, with only the fields gplay reads.
func ProtoManifest(pkg string) []byte {
	attr := concat(
		field(2, []byte("package")), // XmlAttribute.name
		field(3, []byte(pkg)),       // XmlAttribute.value
	)
	element := concat(
		field(3, []byte("manifest")), // XmlElement.name
		field(4, attr),               // XmlElement.attribute
	)
	return field(1, element) // XmlNode.element
}

// field encodes one length-delimited protobuf field.
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

// Android binary-XML constants, mirrored from the reader under test.
const (
	xmlChunkType        = 0x0003
	stringPoolChunkType = 0x0001
	startElemChunkType  = 0x0102
	utf8PoolFlag        = 1 << 8
	noEntry             = 0xFFFFFFFF
	resTypeString       = 0x03
)

// BinaryXMLManifest encodes the compiled AndroidManifest.xml an APK carries:
// a string pool plus a single <manifest package="pkg"> start element. utf16
// selects the older UTF-16 string-pool encoding (both are valid and both
// appear in the wild depending on the aapt generation).
func BinaryXMLManifest(pkg string, utf16Pool bool) []byte {
	strs := []string{"manifest", "package", pkg}
	pool := stringPool(strs, utf16Pool)
	elem := startElement(0 /*manifest*/, 1 /*package*/, 2 /*pkg*/)

	var body bytes.Buffer
	body.Write(pool)
	body.Write(elem)

	var out bytes.Buffer
	writeU16(&out, xmlChunkType)
	writeU16(&out, 8)
	writeU32(&out, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

// startElement encodes a RES_XML_START_ELEMENT with exactly one attribute.
func startElement(nameIdx, attrNameIdx, attrValueIdx uint32) []byte {
	var out bytes.Buffer
	const size = 16 + 20 + 20
	writeU16(&out, startElemChunkType)
	writeU16(&out, 16) // headerSize: ResXMLTree_node
	writeU32(&out, size)
	writeU32(&out, 1)       // lineNumber
	writeU32(&out, noEntry) // comment
	writeU32(&out, noEntry) // ns
	writeU32(&out, nameIdx)
	writeU16(&out, 20) // attributeStart, relative to attrExt
	writeU16(&out, 20) // attributeSize
	writeU16(&out, 1)  // attributeCount
	writeU16(&out, 0)  // idIndex
	writeU16(&out, 0)  // classIndex
	writeU16(&out, 0)  // styleIndex
	// The attribute itself.
	writeU32(&out, noEntry) // ns: package has none
	writeU32(&out, attrNameIdx)
	writeU32(&out, attrValueIdx) // rawValue
	writeU16(&out, 8)            // Res_value.size
	out.WriteByte(0)             // res0
	out.WriteByte(resTypeString)
	writeU32(&out, attrValueIdx) // Res_value.data
	return out.Bytes()
}

// stringPool encodes a RES_STRING_POOL chunk holding strs.
func stringPool(strs []string, utf16Pool bool) []byte {
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
			writeU16(&data, 0) // NUL terminator
			continue
		}
		data.WriteByte(byte(len([]rune(s)))) // character count
		data.WriteByte(byte(len(s)))         // byte count
		data.WriteString(s)
		data.WriteByte(0)
	}
	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}

	const headerSize = 28
	stringsStart := headerSize + 4*len(strs)

	var out bytes.Buffer
	writeU16(&out, stringPoolChunkType)
	writeU16(&out, headerSize)
	writeU32(&out, uint32(stringsStart+data.Len()))
	writeU32(&out, uint32(len(strs)))
	writeU32(&out, 0) // styleCount
	if utf16Pool {
		writeU32(&out, 0)
	} else {
		writeU32(&out, utf8PoolFlag)
	}
	writeU32(&out, uint32(stringsStart))
	writeU32(&out, 0) // stylesStart
	for _, o := range offsets {
		writeU32(&out, o)
	}
	out.Write(data.Bytes())
	return out.Bytes()
}

// utf16Units returns the UTF-16 code units of s (BMP-only in these fixtures).
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
