package artifact

import (
	"encoding/binary"
	"strings"
	"testing"
)

func FuzzParseBinaryXMLPackage(f *testing.F) {
	f.Add(buildFuzzSeedBinaryXML("com.example.app"))
	f.Add(buildFuzzSeedBinaryXML(strings.Repeat("a.", 200) + "z"))
	f.Add([]byte{})
	f.Add([]byte{0x03, 0x00, 0x08, 0x00})
	f.Add([]byte{0x03, 0x00, 0x08, 0x00, 0xff, 0xff, 0xff, 0xff})
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

func FuzzParseProtoManifestPackage(f *testing.F) {
	f.Add(buildFuzzSeedProto("com.example.app"))
	f.Add(buildFuzzSeedProto(""))
	f.Add(buildFuzzSeedProto(strings.Repeat("a.", 200) + "z"))
	f.Add([]byte{})
	f.Add([]byte{0x0a})
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff})
	f.Add([]byte{0x08, 0x01})
	f.Add([]byte{0x0b, 0x00})
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

func buildFuzzSeedProto(pkg string) []byte {
	attr := append(protoField(2, []byte("package")), protoField(3, []byte(pkg))...)
	elem := append(protoField(3, []byte("manifest")), protoField(4, attr)...)
	return protoField(1, elem)
}

func protoField(num int, payload []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}

func buildFuzzSeedBinaryXML(pkg string) []byte {
	strs := []string{"manifest", "package", pkg}

	var data []byte
	offsets := make([]uint32, len(strs))
	for i, s := range strs {
		offsets[i] = uint32(len(data))
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
	elem = append(elem, le32(1)...)
	elem = append(elem, le32(noEntry)...)
	elem = append(elem, le32(noEntry)...)
	elem = append(elem, le32(0)...)
	elem = append(elem, le16(20)...)
	elem = append(elem, le16(20)...)
	elem = append(elem, le16(1)...)
	elem = append(elem, le16(0)...)
	elem = append(elem, le16(0)...)
	elem = append(elem, le16(0)...)
	elem = append(elem, le32(noEntry)...)
	elem = append(elem, le32(1)...)
	elem = append(elem, le32(2)...)
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
