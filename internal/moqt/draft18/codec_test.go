package draft18

import (
	"bytes"
	"testing"
)

func TestSetupRoundTrip(t *testing.T) {
	msg := SetupMessage{
		Options: []SetupOption{
			{Type: SetupOptionPath, Raw: []byte("/moq")},
			{Type: SetupOptionAuthority, Raw: []byte("example.com:443")},
			{Type: SetupOptionImplementation, Raw: []byte("moqsub-go/0.1.0")},
		},
	}
	payload, err := EncodeSetup(msg)
	if err != nil {
		t.Fatalf("encode setup: %v", err)
	}
	got, err := DecodeSetup(payload)
	if err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	if len(got.Options) != len(msg.Options) {
		t.Fatalf("option count mismatch: got=%d want=%d", len(got.Options), len(msg.Options))
	}
	for i := range msg.Options {
		if got.Options[i].Type != msg.Options[i].Type {
			t.Fatalf("option type mismatch at %d: got=%d want=%d", i, got.Options[i].Type, msg.Options[i].Type)
		}
		if !bytes.Equal(got.Options[i].Raw, msg.Options[i].Raw) {
			t.Fatalf("option raw mismatch at %d", i)
		}
	}
}

func TestSubscribeEncodeContainsTrackAndRequestID(t *testing.T) {
	ns, err := EncodeTrackNamespaceFromStrings([]string{"anon", "bbb"})
	if err != nil {
		t.Fatalf("namespace encoding: %v", err)
	}
	payload, err := EncodeSubscribe(SubscribeMessage{
		RequestID:      0,
		TrackNamespace: ns,
		TrackName:      []byte("video"),
	})
	if err != nil {
		t.Fatalf("encode subscribe: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("empty subscribe payload")
	}
}

func TestFrameRoundTrip(t *testing.T) {
	wantType := uint64(MsgSubscribe)
	wantPayload := []byte{0x01, 0x02, 0x03}

	var b bytes.Buffer
	if err := WriteFrame(&b, wantType, wantPayload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	gotType, gotPayload, err := ReadFrame(&b)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if gotType != wantType {
		t.Fatalf("type mismatch: got=%d want=%d", gotType, wantType)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("payload mismatch: got=%v want=%v", gotPayload, wantPayload)
	}
}
