package artifact

import (
	"encoding/binary"
	"strings"
	"testing"
)

// The two manifest readers consume bytes gplay did not produce, from a file
// a CI job may not control, so they get the same fuzz treatment as the API
// error-envelope parser (internal/play/api/fuzz_test.go). The invariant is
// the one the caller depends on: whatever the input, the reader returns
// rather than panicking, and it never invents a package name out of a
// malformed document: a wrong answer here would mean uploading app B's
// build to app A's Edit, the exact failure the preflight exists to prevent.
//
// These are in-package tests: the readers are unexported on purpose (the
// package's contract is Inspect / Preflight), and fuzzing them directly is
// the point.

// FuzzParseBinaryXMLPackage fuzzes the APK binary-XML manifest reader.
func FuzzParseBinaryXMLPackage(f *testing.F) {
	f.Add(buildFuzzSeedBinaryXML("com.example.app"))
	f.Add(buildFuzzSeedBinaryXML(strings.Repeat("a.", 200) + "z"))
	f.Add([]byte{})
	f.Add([]byte{0x03, 0x00, 0x08, 0x00})
	f.Add([]byte{0x03, 0x00, 0x08, 0x00, 0xff, 0xff, 0xff, 0xff})
	// A header claiming a chunk far larger than the document.
	f.Add([]byte{0x03, 0x00, 0x08, 0x00, 0x10, 0x00, 0x00, 0x00, 0x01, 0x00, 0x1c, 0x00, 0xff, 0xff, 0xff, 0x7f})
	f.Add([]byte("not binary xml at all"))

	f.Fuzz(func(t *testing.T, b []byte) {
		pkg, err := parseBinaryXMLPackage(b)
		if err != nil {
			if pkg != "" {
				t.Errorf("parseBinaryXMLPackage returned package %q alongside error %v: a failed parse must yield no package", pkg, err)
			}
			return
		}
		if pkg == "" {
			t.Error("parseBinaryXMLPackage returned a nil error and an empty package: success must carry a value")
		}
	})
}

// FuzzParseProtoManifestPackage fuzzes the AAB aapt2 protobuf manifest reader.
func FuzzParseProtoManifestPackage(f *testing.F) {
	f.Add(buildFuzzSeedProto("com.example.app"))
	f.Add(buildFuzzSeedProto(""))
	f.Add(buildFuzzSeedProto(strings.Repeat("a.", 200) + "z"))
	f.Add([]byte{})
	f.Add([]byte{0x0a})                   // field 1, length-delimited, truncated
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff}) // absurd declared length
	f.Add([]byte{0x08, 0x01})             // varint where a message is expected
	f.Add([]byte{0x0b, 0x00})             // deprecated group wire type
	f.Add([]byte("not protobuf"))

	f.Fuzz(func(t *testing.T, b []byte) {
		pkg, err := parseProtoManifestPackage(b)
		if err != nil {
			if pkg != "" {
				t.Errorf("parseProtoManifestPackage returned package %q alongside error %v: a failed parse must yield no package", pkg, err)
			}
			return
		}
		if pkg == "" {
			t.Error("parseProtoManifestPackage returned a nil error and an empty package: success must carry a value")
		}
	})
}

// TestFuzzSeeds_parseBackToTheirPackage asserts the two well-formed seeds are
// in fact well-formed. A seed that no longer parses is a corpus that fuzzes
// the rejection path only, and it fails silently: the fuzz body accepts any
// error. The long name is the one that matters, since it is the only input in
// the corpus wide enough to reach readLen8's two-byte continuation branch.
func TestFuzzSeeds_parseBackToTheirPackage(t *testing.T) {
	longPkg := strings.Repeat("a.", 200) + "z"
	if len(longPkg) <= 0x7F {
		t.Fatalf("seed package is %d bytes, need more than 127", len(longPkg))
	}
	for _, pkg := range []string{"com.example.app", longPkg} {
		got, err := parseBinaryXMLPackage(buildFuzzSeedBinaryXML(pkg))
		if err != nil || got != pkg {
			t.Errorf("parseBinaryXMLPackage(seed %d bytes) = %q, %v; want %q, nil", len(pkg), got, err, pkg)
		}
		if got, err := parseProtoManifestPackage(buildFuzzSeedProto(pkg)); err != nil || got != pkg {
			t.Errorf("parseProtoManifestPackage(seed %d bytes) = %q, %v; want %q, nil", len(pkg), got, err, pkg)
		}
	}
}

// The seed builders duplicate a few bytes of artifacttest on purpose: this is
// an in-package test file, and importing artifacttest (which imports testing
// helpers keyed to *testing.T) from a fuzz seed would be the wrong direction.

func buildFuzzSeedProto(pkg string) []byte {
	attr := append(protoField(2, []byte("package")), protoField(3, []byte(pkg))...)
	elem := append(protoField(3, []byte("manifest")), protoField(4, attr)...)
	return protoField(1, elem)
}

func protoField(num int, payload []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num)<<3|2)
	// A varint length, not a byte: a payload over 127 bytes (the long package
	// seed) would otherwise be encoded as garbage and seed nothing.
	out = binary.AppendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}

func buildFuzzSeedBinaryXML(pkg string) []byte {
	strs := []string{"manifest", "package", pkg}

	var data []byte
	offsets := make([]uint32, len(strs))
	for i, s := range strs {
		offsets[i] = uint32(len(data))
		// aapt's length prefix, one byte up to 127 and two above: the long
		// seed exists precisely to drive readLen8's continuation branch, so
		// writing a truncated byte would make it seed a malformed document
		// and cover nothing.
		data = appendLen8(data, len([]rune(s)))
		data = appendLen8(data, len(s))
		data = append(data, s...)
		data = append(data, 0)
	}
	for len(data)%4 != 0 {
		data = append(data, 0)
	}

	const poolHeader = 28
	stringsStart := poolHeader + 4*len(strs)
	pool := le16(chunkTypeStringPool)
	pool = append(pool, le16(poolHeader)...)
	pool = append(pool, le32(uint32(stringsStart+len(data)))...)
	pool = append(pool, le32(uint32(len(strs)))...)
	pool = append(pool, le32(0)...)
	pool = append(pool, le32(utf8Flag)...)
	pool = append(pool, le32(uint32(stringsStart))...)
	pool = append(pool, le32(0)...)
	for _, o := range offsets {
		pool = append(pool, le32(o)...)
	}
	pool = append(pool, data...)

	elem := le16(chunkTypeXMLStartElem)
	elem = append(elem, le16(16)...)
	elem = append(elem, le32(56)...)
	elem = append(elem, le32(1)...)       // lineNumber
	elem = append(elem, le32(noEntry)...) // comment
	elem = append(elem, le32(noEntry)...) // ns
	elem = append(elem, le32(0)...)       // name → "manifest"
	elem = append(elem, le16(20)...)      // attributeStart
	elem = append(elem, le16(20)...)      // attributeSize
	elem = append(elem, le16(1)...)       // attributeCount
	elem = append(elem, le16(0)...)       // idIndex
	elem = append(elem, le16(0)...)       // classIndex
	elem = append(elem, le16(0)...)       // styleIndex
	elem = append(elem, le32(noEntry)...) // attr ns
	elem = append(elem, le32(1)...)       // attr name → "package"
	elem = append(elem, le32(2)...)       // attr rawValue → pkg
	elem = append(elem, le16(8)...)
	elem = append(elem, 0, typeString)
	elem = append(elem, le32(2)...)

	out := le16(chunkTypeXML)
	out = append(out, le16(8)...)
	out = append(out, le32(uint32(8+len(pool)+len(elem)))...)
	out = append(out, pool...)
	return append(out, elem...)
}

func appendLen8(dst []byte, n int) []byte {
	if n > 0x7F {
		dst = append(dst, byte(0x80|n>>8))
	}
	return append(dst, byte(n))
}

func le16(v int) []byte { return []byte{byte(v), byte(v >> 8)} }

func le32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}
