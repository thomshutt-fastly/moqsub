package draft18

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const maxFrameLen = 1<<16 - 1

// WriteFrame serializes a MOQT Control Message: a varint message type,
// a fixed 16-bit big-endian length, then the message body.
func WriteFrame(w io.Writer, msgType uint64, payload []byte) error {
	if len(payload) > maxFrameLen {
		return fmt.Errorf("payload too large: %d", len(payload))
	}
	buf := make([]byte, 0, 16+len(payload))
	buf = AppendVarint(buf, msgType)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(payload)))
	buf = append(buf, payload...)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads a full MOQT Control Message, including the message type.
func ReadFrame(r io.Reader) (msgType uint64, payload []byte, err error) {
	msgType, err = ReadVarint(r)
	if err != nil {
		return 0, nil, err
	}
	payload, err = ReadFrameBody(r)
	if err != nil {
		return 0, nil, err
	}
	return msgType, payload, nil
}

// ReadFrameBody reads the 16-bit length and body of a MOQT Control Message,
// for callers that have already consumed the message type varint (for example
// the first SETUP message on a control stream, whose type doubles as the
// unidirectional stream type).
func ReadFrameBody(r io.Reader) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint16(lenBuf[:])
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func EncodeSetup(msg SetupMessage) ([]byte, error) {
	return encodeKeyValuePairs(msg.Options)
}

func DecodeSetup(payload []byte) (SetupMessage, error) {
	pairs, err := decodeKeyValuePairs(payload)
	if err != nil {
		return SetupMessage{}, err
	}
	opts := make([]SetupOption, 0, len(pairs))
	for _, p := range pairs {
		opts = append(opts, SetupOption{Type: p.Type, Raw: p.Raw})
	}
	return SetupMessage{Options: opts}, nil
}

func EncodeSubscribe(msg SubscribeMessage) ([]byte, error) {
	var payload []byte
	payload = AppendVarint(payload, msg.RequestID)
	ns, err := encodeTrackNamespace(msg.TrackNamespace)
	if err != nil {
		return nil, err
	}
	payload = append(payload, ns...)
	payload = AppendVarint(payload, uint64(len(msg.TrackName)))
	payload = append(payload, msg.TrackName...)
	if len(msg.ParametersRaw) == 0 {
		payload = AppendVarint(payload, 0)
	} else {
		num, err := countKeyValuePairs(msg.ParametersRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid parameters payload: %w", err)
		}
		payload = AppendVarint(payload, num)
		payload = append(payload, msg.ParametersRaw...)
	}
	return payload, nil
}

// DecodeSubscribeOK parses a SUBSCRIBE_OK body (draft-18 §10.8). It is sent on
// the bidirectional request stream, so it carries no Request ID: the body is
// Track Alias, Number of Parameters, Parameters, then Track Properties. We
// decode the leading fields and keep the remainder raw for logging.
func DecodeSubscribeOK(payload []byte) (SubscribeOKMessage, error) {
	r := bytes.NewReader(payload)
	alias, err := ReadVarint(r)
	if err != nil {
		return SubscribeOKMessage{}, fmt.Errorf("track alias: %w", err)
	}
	numParams, err := ReadVarint(r)
	if err != nil {
		return SubscribeOKMessage{}, fmt.Errorf("number of parameters: %w", err)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		return SubscribeOKMessage{}, err
	}
	return SubscribeOKMessage{
		TrackAlias:      alias,
		NumParams:       numParams,
		TrackProperties: rest,
	}, nil
}

// DecodeRequestError parses a REQUEST_ERROR body (draft-18 §10.6.2). It is sent
// on the bidirectional request stream, so it carries no Request ID: the body is
// Error Code, Retry Interval, Error Reason (Reason Phrase), then an optional
// Redirect (present only when Error Code is REDIRECT).
func DecodeRequestError(payload []byte) (RequestErrorMessage, error) {
	r := bytes.NewReader(payload)
	code, err := ReadVarint(r)
	if err != nil {
		return RequestErrorMessage{}, fmt.Errorf("error code: %w", err)
	}
	retry, err := ReadVarint(r)
	if err != nil {
		return RequestErrorMessage{}, fmt.Errorf("retry interval: %w", err)
	}
	reasonLen, err := ReadVarint(r)
	if err != nil {
		return RequestErrorMessage{}, fmt.Errorf("reason length: %w", err)
	}
	if reasonLen > 1024 {
		return RequestErrorMessage{}, fmt.Errorf("reason phrase too long: %d", reasonLen)
	}
	reason := make([]byte, reasonLen)
	if _, err := io.ReadFull(r, reason); err != nil {
		return RequestErrorMessage{}, fmt.Errorf("reason bytes: %w", err)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		return RequestErrorMessage{}, err
	}
	return RequestErrorMessage{
		Code:          code,
		RetryInterval: retry,
		Reason:        string(reason),
		Redirect:      rest,
	}, nil
}

func EncodeTrackNamespaceFromStrings(fields []string) ([][]byte, error) {
	ns := make([][]byte, 0, len(fields))
	for _, f := range fields {
		if f == "" {
			return nil, errors.New("track namespace field cannot be empty")
		}
		ns = append(ns, []byte(f))
	}
	return ns, nil
}

func encodeTrackNamespace(fields [][]byte) ([]byte, error) {
	if len(fields) > 32 {
		return nil, fmt.Errorf("too many namespace fields: %d", len(fields))
	}
	var b []byte
	b = AppendVarint(b, uint64(len(fields)))
	fullLen := 0
	for _, f := range fields {
		if len(f) == 0 {
			return nil, errors.New("namespace field must not be empty")
		}
		fullLen += len(f)
		b = AppendVarint(b, uint64(len(f)))
		b = append(b, f...)
	}
	if fullLen > 4096 {
		return nil, fmt.Errorf("namespace length too large: %d", fullLen)
	}
	return b, nil
}

type kvPair struct {
	Type uint64
	Raw  []byte
}

func encodeKeyValuePairs(options []SetupOption) ([]byte, error) {
	var out []byte
	var prev uint64
	for i, opt := range options {
		if i == 0 {
			if opt.Type < prev {
				return nil, errors.New("option type ordering invalid")
			}
		} else if opt.Type <= prev {
			return nil, errors.New("option types must be strictly increasing")
		}
		delta := opt.Type - prev
		out = AppendVarint(out, delta)
		if opt.Type%2 == 1 {
			out = AppendVarint(out, uint64(len(opt.Raw)))
			out = append(out, opt.Raw...)
		} else {
			if len(opt.Raw) == 0 {
				out = AppendVarint(out, 0)
			} else {
				val, n, err := ParseVarint(opt.Raw)
				if err != nil || n <= 0 || n != len(opt.Raw) {
					return nil, fmt.Errorf("even option %d must be a varint", opt.Type)
				}
				out = AppendVarint(out, val)
			}
		}
		prev = opt.Type
	}
	return out, nil
}

func decodeKeyValuePairs(payload []byte) ([]kvPair, error) {
	r := bytes.NewReader(payload)
	var out []kvPair
	var prevType uint64
	for r.Len() > 0 {
		delta, err := ReadVarint(r)
		if err != nil {
			return nil, fmt.Errorf("delta type: %w", err)
		}
		if delta > ^uint64(0)-prevType {
			return nil, errors.New("delta overflows type")
		}
		t := prevType + delta
		var raw []byte
		if t%2 == 1 {
			l, err := ReadVarint(r)
			if err != nil {
				return nil, fmt.Errorf("length for type %d: %w", t, err)
			}
			if l > uint64(r.Len()) {
				return nil, fmt.Errorf("length exceeds payload for type %d", t)
			}
			raw = make([]byte, l)
			if _, err := io.ReadFull(r, raw); err != nil {
				return nil, fmt.Errorf("value for type %d: %w", t, err)
			}
		} else {
			val, err := ReadVarint(r)
			if err != nil {
				return nil, fmt.Errorf("varint value for type %d: %w", t, err)
			}
			raw = AppendVarint(nil, val)
		}
		out = append(out, kvPair{Type: t, Raw: raw})
		prevType = t
	}
	return out, nil
}

func countKeyValuePairs(payload []byte) (uint64, error) {
	pairs, err := decodeKeyValuePairs(payload)
	if err != nil {
		return 0, err
	}
	return uint64(len(pairs)), nil
}
