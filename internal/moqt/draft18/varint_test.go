package draft18

import (
	"bytes"
	"testing"
)

// Vectors from draft-ietf-moq-transport-18 §1.4.1 (leading-1-bits varint).
func TestVarintSpecVectors(t *testing.T) {
	cases := []struct {
		value uint64
		bytes []byte
	}{
		{0, []byte{0x00}},
		{37, []byte{0x25}},
		{0x7F, []byte{0x7F}},
		{15293, []byte{0xBB, 0xBD}},
		{0x2F00, []byte{0xAF, 0x00}}, // SETUP type / control stream type
		{226442877, []byte{0xED, 0x7F, 0x3E, 0x7D}},
		{2893212287960, []byte{0xFA, 0xA1, 0xA0, 0xE4, 0x03, 0xD8}},
		{151288809941952, []byte{0xFC, 0x89, 0x98, 0xAB, 0xC6, 0x6B, 0xC0}},
		{70423237261249041, []byte{0xFE, 0xFA, 0x31, 0x8F, 0xA8, 0xE3, 0xCA, 0x11}},
		{^uint64(0), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
	}
	for _, c := range cases {
		got := AppendVarint(nil, c.value)
		if !bytes.Equal(got, c.bytes) {
			t.Fatalf("encode %d: got %x want %x", c.value, got, c.bytes)
		}
		v, n, err := ParseVarint(c.bytes)
		if err != nil {
			t.Fatalf("parse %x: %v", c.bytes, err)
		}
		if v != c.value || n != len(c.bytes) {
			t.Fatalf("parse %x: got (%d,%d) want (%d,%d)", c.bytes, v, n, c.value, len(c.bytes))
		}
		rv, err := ReadVarint(bytes.NewReader(c.bytes))
		if err != nil {
			t.Fatalf("read %x: %v", c.bytes, err)
		}
		if rv != c.value {
			t.Fatalf("read %x: got %d want %d", c.bytes, rv, c.value)
		}
	}
}

// Non-minimal encodings must decode (spec allows them).
func TestVarintNonMinimalDecode(t *testing.T) {
	v, n, err := ParseVarint([]byte{0x80, 0x25})
	if err != nil {
		t.Fatalf("parse non-minimal: %v", err)
	}
	if v != 37 || n != 2 {
		t.Fatalf("non-minimal: got (%d,%d) want (37,2)", v, n)
	}
}

// The control stream must begin with the SETUP message whose type varint is
// 0x2F00, encoded as [0xAF, 0x00], followed by a 16-bit length.
func TestSetupControlStreamWireFormat(t *testing.T) {
	payload, err := EncodeSetup(SetupMessage{
		Options: []SetupOption{{Type: SetupOptionPath, Raw: []byte("/moq")}},
	})
	if err != nil {
		t.Fatalf("encode setup: %v", err)
	}
	var b bytes.Buffer
	if err := WriteFrame(&b, MsgSetup, payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	wire := b.Bytes()
	if wire[0] != 0xAF || wire[1] != 0x00 {
		t.Fatalf("setup type prefix: got %x %x want AF 00", wire[0], wire[1])
	}
	// Bytes 2..3 are the u16 big-endian length of the payload.
	gotLen := int(wire[2])<<8 | int(wire[3])
	if gotLen != len(payload) {
		t.Fatalf("length field: got %d want %d", gotLen, len(payload))
	}
}
