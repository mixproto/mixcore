// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package wingproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// NodeType is the type of a node from its definition flags (WG-F33, bits 4-7).
type NodeType uint8

// Node types (WG-F33).
const (
	TypeNode       NodeType = 0
	TypeLinFloat   NodeType = 1
	TypeLogFloat   NodeType = 2
	TypeFader      NodeType = 3
	TypeInt        NodeType = 4
	TypeStringEnum NodeType = 5
	TypeFloatEnum  NodeType = 6
	TypeString     NodeType = 7
)

// Unit is the unit of a node from its definition flags (WG-F33, bits 0-3).
type Unit uint8

// Units (WG-F33).
const (
	UnitNone    Unit = 0
	UnitDB      Unit = 1
	UnitPercent Unit = 2
	UnitMS      Unit = 3
	UnitHz      Unit = 4
	UnitMetres  Unit = 5
	UnitSeconds Unit = 6
	UnitOctaves Unit = 7
)

// Definition flag bits (WG-F33).
const (
	flagUnitMask  = 0x000F
	flagTypeShift = 4
	flagTypeMask  = 0x000F
	flagReadOnly  = 1 << 9
)

// MaxEnumItems caps the items of one enum definition. The largest real
// enums have a few dozen items (wing.md 3.4: 17 source groups, 24 process
// orders); more is rejected, never allocated (review H2, security re-check 3).
const MaxEnumItems = 256

// ErrBadDef means a definition body does not follow WG-F33.
var ErrBadDef = errors.New("wingproto: malformed node definition")

// EnumItem is one item of a string enum (Name, Long) or a float enum (Value, Long).
type EnumItem struct {
	Name  string
	Value float32
	Long  string
}

// Def is one node definition (WG-F33, firmware 3.x layout with parent).
type Def struct {
	Parent   uint32
	Hash     uint32
	Index    uint16
	Name     string
	LongName string
	Type     NodeType
	Unit     Unit
	ReadOnly bool
	// Min, Max and Steps: lin and log floats.
	Min, Max float32
	Steps    int32
	// IntMin, IntMax: ints.
	IntMin, IntMax int32
	// Items: string and float enums.
	Items []EnumItem
	// MaxLen: strings.
	MaxLen uint16
}

// defReader reads a definition body with bounds checks.
type defReader struct {
	b   []byte
	off int
	bad bool
}

func (r *defReader) take(n int) []byte {
	if r.bad || n < 0 || r.off+n > len(r.b) {
		r.bad = true
		return nil
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out
}

func (r *defReader) u8() int {
	if b := r.take(1); b != nil {
		return int(b[0])
	}
	return 0
}

func (r *defReader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.BigEndian.Uint16(b)
	}
	return 0
}

func (r *defReader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.BigEndian.Uint32(b)
	}
	return 0
}

func (r *defReader) f32() float32 { return math.Float32frombits(r.u32()) }

// str reads len.b and that many bytes.
func (r *defReader) str() string { return string(r.take(r.u8())) }

// ParseDef decodes a definition body (the bytes after DF len): parent.l
// hash.l index.w namelen.b name longnamelen.b longname flags.w, then the
// type's fields (WG-F33). Trailing bytes are ignored.
func ParseDef(body []byte) (Def, error) {
	r := defReader{b: body}
	d := Def{Parent: r.u32(), Hash: r.u32(), Index: r.u16()}
	d.Name, d.LongName = r.str(), r.str()
	flags := r.u16()
	d.Unit = Unit(flags & flagUnitMask)
	d.Type = NodeType((flags >> flagTypeShift) & flagTypeMask)
	d.ReadOnly = flags&flagReadOnly != 0
	switch d.Type {
	case TypeLinFloat, TypeLogFloat:
		d.Min, d.Max, d.Steps = r.f32(), r.f32(), int32(r.u32())
	case TypeInt:
		d.IntMin, d.IntMax = int32(r.u32()), int32(r.u32())
	case TypeStringEnum, TypeFloatEnum:
		d.Items = r.items(d.Type)
	case TypeString:
		d.MaxLen = r.u16()
	case TypeNode, TypeFader:
	default:
		return Def{}, fmt.Errorf("%w: type %d", ErrBadDef, d.Type)
	}
	if r.bad {
		return Def{}, fmt.Errorf("%w: %d bytes", ErrBadDef, len(body))
	}
	return d, nil
}

// minItemBytes is the smallest encoded enum item (two empty strings).
const minItemBytes = 2

// items reads count.w and the items of a string or float enum. The count is
// checked against the bytes left before anything is allocated.
func (r *defReader) items(t NodeType) []EnumItem {
	n := int(r.u16())
	if r.bad || n > MaxEnumItems || n*minItemBytes > len(r.b)-r.off {
		r.bad = true
		return nil
	}
	out := make([]EnumItem, 0, n)
	for range n {
		var it EnumItem
		if t == TypeStringEnum {
			it.Name = r.str()
		} else {
			it.Value = r.f32()
		}
		it.Long = r.str()
		if r.bad {
			return nil
		}
		out = append(out, it)
	}
	return out
}

// AppendDefBody encodes d as a definition body (the inverse of ParseDef),
// for tests and tools. Names longer than 255 bytes are ErrValue.
func AppendDefBody(dst []byte, d Def) ([]byte, error) {
	if len(d.Name) > math.MaxUint8 || len(d.LongName) > math.MaxUint8 {
		return dst, fmt.Errorf("%w: name too long", ErrValue)
	}
	dst = binary.BigEndian.AppendUint32(dst, d.Parent)
	dst = binary.BigEndian.AppendUint32(dst, d.Hash)
	dst = binary.BigEndian.AppendUint16(dst, d.Index)
	dst = appendStr8(appendStr8(dst, d.Name), d.LongName)
	flags := uint16(d.Unit)&flagUnitMask | (uint16(d.Type)&flagTypeMask)<<flagTypeShift
	if d.ReadOnly {
		flags |= flagReadOnly
	}
	dst = binary.BigEndian.AppendUint16(dst, flags)
	return appendDefFields(dst, d)
}

func appendDefFields(dst []byte, d Def) ([]byte, error) {
	switch d.Type {
	case TypeLinFloat, TypeLogFloat:
		dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(d.Min))
		dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(d.Max))
		dst = binary.BigEndian.AppendUint32(dst, uint32(d.Steps))
	case TypeInt:
		dst = binary.BigEndian.AppendUint32(dst, uint32(d.IntMin))
		dst = binary.BigEndian.AppendUint32(dst, uint32(d.IntMax))
	case TypeStringEnum, TypeFloatEnum:
		if len(d.Items) > math.MaxUint16 {
			return dst, fmt.Errorf("%w: %d enum items", ErrValue, len(d.Items))
		}
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(d.Items)))
		for _, it := range d.Items {
			if len(it.Name) > math.MaxUint8 || len(it.Long) > math.MaxUint8 {
				return dst, fmt.Errorf("%w: enum item too long", ErrValue)
			}
			if d.Type == TypeStringEnum {
				dst = appendStr8(dst, it.Name)
			} else {
				dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(it.Value))
			}
			dst = appendStr8(dst, it.Long)
		}
	case TypeString:
		dst = binary.BigEndian.AppendUint16(dst, d.MaxLen)
	}
	return dst, nil
}

func appendStr8(dst []byte, s string) []byte { return append(append(dst, byte(len(s))), s...) }

// AppendDefToken wraps a definition body as a DF response token (WG-F33):
// DF len.w body, or DF 0000 len.l body when the body is 64 KiB or longer.
func AppendDefToken(dst, body []byte) []byte {
	dst = append(dst, tokDefResponse)
	if len(body) > 0 && len(body) <= math.MaxUint16 {
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(body)))
	} else {
		dst = binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint16(dst, 0), uint32(len(body)))
	}
	return append(dst, body...)
}
