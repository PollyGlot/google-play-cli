package artifact

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// This file implements the minimum of aapt2's protobuf XML encoding (the
// AndroidManifest.xml inside an AAB, `base/manifest/AndroidManifest.xml`)
// needed to read the declared package name.
//
// Only three messages of aapt2's Resources.proto matter, and only by field
// number, so no generated code and no protobuf runtime is linked in
// (ADR-0007 keeps the binary a single static build):
//
//	XmlNode      { XmlElement element = 1; string text = 2; }
//	XmlElement   { ... string name = 3; repeated XmlAttribute attribute = 4; }
//	XmlAttribute { string namespace_uri = 1; string name = 2; string value = 3; }
//
// As with the binary-XML reader, the bytes are untrusted: the only invariant
// is that this never panics, and every failure degrades the caller to a
// container-only check.

// Protobuf wire types; only the two this reader can encounter on the fields
// it cares about are named.
const (
	wireVarint = 0
	wire64     = 1
	wireBytes  = 2
	wire32     = 5
)

var errBadWire = errors.New("malformed protobuf")

// parseProtoManifestPackage returns the `package` attribute of the root
// <manifest> element of an aapt2 protobuf AndroidManifest.xml.
func parseProtoManifestPackage(b []byte) (string, error) {
	var element []byte
	if err := eachField(b, func(num int, wire int, data []byte, _ uint64) error {
		if num == 1 && wire == wireBytes {
			element = data
		}
		return nil
	}); err != nil {
		return "", err
	}
	if element == nil {
		return "", errors.New("root XmlNode carries no element")
	}

	var (
		name  string
		attrs [][]byte
	)
	if err := eachField(element, func(num int, wire int, data []byte, _ uint64) error {
		if wire != wireBytes {
			return nil
		}
		switch num {
		case 3:
			name = string(data)
		case 4:
			attrs = append(attrs, data)
		}
		return nil
	}); err != nil {
		return "", err
	}
	if name != "manifest" {
		return "", fmt.Errorf("root element is %q, not <manifest>", name)
	}

	for _, a := range attrs {
		var ns, attrName, value string
		if err := eachField(a, func(num int, wire int, data []byte, _ uint64) error {
			if wire != wireBytes {
				return nil
			}
			switch num {
			case 1:
				ns = string(data)
			case 2:
				attrName = string(data)
			case 3:
				value = string(data)
			}
			return nil
		}); err != nil {
			return "", err
		}
		// package carries no namespace, exactly as in the binary-XML form.
		if ns == "" && attrName == "package" {
			if value == "" {
				return "", errors.New("<manifest> package attribute is empty")
			}
			return value, nil
		}
	}
	return "", errors.New("<manifest> declares no package attribute")
}

// eachField walks a protobuf message, calling fn for every top-level field.
// Length-delimited fields hand fn their payload; varints hand it their
// value. Nothing recurses: the caller decides what to descend into, so a
// hostile deeply-nested message cannot blow the stack.
func eachField(b []byte, fn func(num, wire int, data []byte, v uint64) error) error {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return errBadWire
		}
		b = b[n:]
		num := int(key >> 3)
		wire := int(key & 0x7)
		if num <= 0 {
			return errBadWire
		}
		switch wire {
		case wireVarint:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return errBadWire
			}
			b = b[n:]
			if err := fn(num, wire, nil, v); err != nil {
				return err
			}
		case wire64:
			if len(b) < 8 {
				return errBadWire
			}
			v := binary.LittleEndian.Uint64(b[:8])
			b = b[8:]
			if err := fn(num, wire, nil, v); err != nil {
				return err
			}
		case wire32:
			if len(b) < 4 {
				return errBadWire
			}
			v := uint64(binary.LittleEndian.Uint32(b[:4]))
			b = b[4:]
			if err := fn(num, wire, nil, v); err != nil {
				return err
			}
		case wireBytes:
			ln, n := binary.Uvarint(b)
			if n <= 0 {
				return errBadWire
			}
			b = b[n:]
			// The declared length is attacker-controlled: compare in uint64
			// so a huge value cannot wrap into a valid-looking int.
			if ln > uint64(len(b)) {
				return errBadWire
			}
			data := b[:ln]
			b = b[ln:]
			if err := fn(num, wire, data, 0); err != nil {
				return err
			}
		default:
			// Groups (3/4) are deprecated and never appear in aapt2 output.
			return errBadWire
		}
	}
	return nil
}
