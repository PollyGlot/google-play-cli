package artifact

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	wireVarint = 0
	wire64     = 1
	wireBytes  = 2
	wire32     = 5
)

var errBadWire = errors.New("malformed protobuf")

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
		if ns == "" && attrName == "package" {
			if value == "" {
				return "", errors.New("<manifest> package attribute is empty")
			}
			return value, nil
		}
	}
	return "", errors.New("<manifest> declares no package attribute")
}

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
			if ln > uint64(len(b)) {
				return errBadWire
			}
			data := b[:ln]
			b = b[ln:]
			if err := fn(num, wire, data, 0); err != nil {
				return err
			}
		default:
			return errBadWire
		}
	}
	return nil
}
