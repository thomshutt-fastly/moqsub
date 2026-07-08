package draft18

import (
	"encoding/binary"
	"io"
	"math/bits"
)

// MoQ Transport (draft-ietf-moq-transport-17+ §1.4.1) uses a "leading-1-bits"
// variable-length integer encoding. This is NOT the same as the QUIC transport
// varint (which uses the two most-significant bits as a length prefix). The
// number of leading 1-bits in the first byte determines the total length:
//
//	| Leading bits | Total bytes | Max value         |
//	| 0            | 1           | 127               |
//	| 10           | 2           | 16383             |
//	| 110          | 3           | 2097151           |
//	| 1110         | 4           | 268435455         |
//	| ...          | ...         | ...               |
//	| 11111111     | 9           | u64::MAX          |

func varintSize(x uint64) int {
	switch {
	case x <= 0x7F:
		return 1
	case x <= 0x3FFF:
		return 2
	case x <= 0x1FFFFF:
		return 3
	case x <= 0x0FFFFFFF:
		return 4
	case x <= 0x07FFFFFFFF:
		return 5
	case x <= 0x03FFFFFFFFFF:
		return 6
	case x <= 0x01FFFFFFFFFFFF:
		return 7
	case x <= 0x00FFFFFFFFFFFFFF:
		return 8
	default:
		return 9
	}
}

// AppendVarint appends v to b using the MoQ leading-1-bits varint encoding.
func AppendVarint(b []byte, v uint64) []byte {
	n := varintSize(v)
	switch n {
	case 1:
		return append(b, byte(v))
	case 9:
		b = append(b, 0xFF)
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], v)
		return append(b, tmp[:]...)
	default:
		firstData := byte(v >> uint((n-1)*8))
		mask := (uint16(1) << uint(9-n)) - 1
		tag := byte(^mask)
		b = append(b, tag|firstData)
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], v)
		return append(b, tmp[8-(n-1):]...)
	}
}

// ParseVarint decodes a MoQ varint from the front of b, returning the value and
// the number of bytes consumed.
func ParseVarint(b []byte) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	first := b[0]
	ones := bits.LeadingZeros8(^first)
	extra := ones
	if extra > 8 {
		extra = 8
	}
	total := 1 + extra
	if len(b) < total {
		return 0, 0, io.ErrUnexpectedEOF
	}
	value := decodeVarintBytes(first, b[1:total], extra)
	return value, total, nil
}

// ReadVarint reads a single MoQ varint from r.
func ReadVarint(r io.Reader) (uint64, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, err
	}
	ones := bits.LeadingZeros8(^first[0])
	extra := ones
	if extra > 8 {
		extra = 8
	}
	if extra == 0 {
		return uint64(first[0] & 0x7F), nil
	}
	rest := make([]byte, extra)
	if _, err := io.ReadFull(r, rest); err != nil {
		return 0, err
	}
	return decodeVarintBytes(first[0], rest, extra), nil
}

func decodeVarintBytes(first byte, rest []byte, extra int) uint64 {
	switch extra {
	case 0:
		return uint64(first & 0x7F)
	case 8:
		return binary.BigEndian.Uint64(rest)
	case 7:
		var buf [8]byte
		copy(buf[1:], rest)
		return binary.BigEndian.Uint64(buf[:])
	default:
		n := extra
		mask := byte(0xFF) >> uint(n+1)
		var buf [8]byte
		buf[8-1-n] = first & mask
		copy(buf[8-n:], rest)
		return binary.BigEndian.Uint64(buf[:])
	}
}
