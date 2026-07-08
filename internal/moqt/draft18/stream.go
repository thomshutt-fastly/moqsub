package draft18

import (
	"fmt"
	"io"
)

// Subgroup stream header types as implemented by moq-rs draft-18-dev
// (moq-transport/src/data/header.rs). Note this is a narrower, enumerated set
// than the bit-oriented range in the IETF draft text.
const (
	StreamTypeSubgroupZeroId                   = 0x10
	StreamTypeSubgroupZeroIdExt                = 0x11
	StreamTypeSubgroupFirstObjectId            = 0x12
	StreamTypeSubgroupFirstObjectIdExt         = 0x13
	StreamTypeSubgroupId                       = 0x14
	StreamTypeSubgroupIdExt                    = 0x15
	StreamTypeSubgroupZeroIdEndOfGroup         = 0x18
	StreamTypeSubgroupZeroIdExtEndOfGroup      = 0x19
	StreamTypeSubgroupFirstObjectIdEndOfGroup  = 0x1a
	StreamTypeSubgroupFirstObjectIdExtEndGroup = 0x1b
	StreamTypeSubgroupIdEndOfGroup             = 0x1c
	StreamTypeSubgroupIdExtEndOfGroup          = 0x1d
)

func IsSubgroupStreamType(t uint64) bool {
	return (t >= 0x10 && t <= 0x15) || (t >= 0x18 && t <= 0x1d)
}

func subgroupHasSubgroupID(t uint64) bool {
	switch t {
	case StreamTypeSubgroupId, StreamTypeSubgroupIdExt,
		StreamTypeSubgroupIdEndOfGroup, StreamTypeSubgroupIdExtEndOfGroup:
		return true
	}
	return false
}

func subgroupHasExtensions(t uint64) bool {
	switch t {
	case StreamTypeSubgroupZeroIdExt, StreamTypeSubgroupFirstObjectIdExt,
		StreamTypeSubgroupIdExt, StreamTypeSubgroupZeroIdExtEndOfGroup,
		StreamTypeSubgroupFirstObjectIdExtEndGroup, StreamTypeSubgroupIdExtEndOfGroup:
		return true
	}
	return false
}

func subgroupUsesFirstObjectID(t uint64) bool {
	switch t {
	case StreamTypeSubgroupFirstObjectId, StreamTypeSubgroupFirstObjectIdExt,
		StreamTypeSubgroupFirstObjectIdEndOfGroup, StreamTypeSubgroupFirstObjectIdExtEndGroup:
		return true
	}
	return false
}

func ParseSubgroupHeader(r io.Reader, streamType uint64) (SubgroupHeader, error) {
	if !IsSubgroupStreamType(streamType) {
		return SubgroupHeader{}, fmt.Errorf("not subgroup stream type: 0x%x", streamType)
	}
	h := SubgroupHeader{
		Type:              streamType,
		HasSubgroupID:     subgroupHasSubgroupID(streamType),
		HasExtensions:     subgroupHasExtensions(streamType),
		UsesFirstObjectID: subgroupUsesFirstObjectID(streamType),
		EndOfGroup:        streamType >= 0x18,
	}

	var err error
	h.TrackAlias, err = ReadVarint(r)
	if err != nil {
		return SubgroupHeader{}, fmt.Errorf("track alias: %w", err)
	}
	h.GroupID, err = ReadVarint(r)
	if err != nil {
		return SubgroupHeader{}, fmt.Errorf("group id: %w", err)
	}
	if h.HasSubgroupID {
		h.SubgroupID, err = ReadVarint(r)
		if err != nil {
			return SubgroupHeader{}, fmt.Errorf("subgroup id: %w", err)
		}
	}
	// Publisher priority is always present (single byte).
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return SubgroupHeader{}, fmt.Errorf("publisher priority: %w", err)
	}
	h.PublisherPriority = b[0]
	return h, nil
}

type SubgroupObjectReader struct {
	r             io.Reader
	hasExtensions bool
	prevObjectID  *uint64
}

func NewSubgroupObjectReader(r io.Reader, header SubgroupHeader) *SubgroupObjectReader {
	return &SubgroupObjectReader{
		r:             r,
		hasExtensions: header.HasExtensions,
	}
}

func (s *SubgroupObjectReader) Next() (SubgroupObject, error) {
	delta, err := ReadVarint(s.r)
	if err != nil {
		return SubgroupObject{}, err
	}
	objID := delta
	if s.prevObjectID != nil {
		objID = *s.prevObjectID + delta + 1
	}

	obj := SubgroupObject{
		ObjectIDDelta: delta,
		ObjectID:      objID,
	}

	if s.hasExtensions {
		// Extension headers: a byte-length-prefixed block of KVPs.
		extLen, err := ReadVarint(s.r)
		if err != nil {
			return SubgroupObject{}, fmt.Errorf("extension headers length: %w", err)
		}
		if extLen > 0 {
			obj.PropertiesRaw = make([]byte, extLen)
			if _, err := io.ReadFull(s.r, obj.PropertiesRaw); err != nil {
				return SubgroupObject{}, fmt.Errorf("extension headers payload: %w", err)
			}
		}
	}

	payloadLen, err := ReadVarint(s.r)
	if err != nil {
		return SubgroupObject{}, fmt.Errorf("payload length: %w", err)
	}
	obj.PayloadLen = payloadLen
	if payloadLen == 0 {
		status, err := ReadVarint(s.r)
		if err != nil {
			return SubgroupObject{}, fmt.Errorf("object status: %w", err)
		}
		obj.ObjectStatus = status
		obj.IsStatusObject = true
	} else {
		obj.Payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(s.r, obj.Payload); err != nil {
			return SubgroupObject{}, fmt.Errorf("object payload: %w", err)
		}
		obj.ObjectStatus = ObjectStatusNormal
	}

	s.prevObjectID = &objID
	return obj, nil
}
