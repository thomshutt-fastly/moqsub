package app

import "testing"

func TestResolveMediaTrack(t *testing.T) {
	root := catalogRoot{
		Version: 1,
		Tracks: []catalogTrack{
			{Name: catalogTrackName},
			{Name: "1.m4s", InitTrack: "0.mp4", SelectionParams: catalogSelectionParam{Codec: "avc1.640028"}},
		},
	}
	name, err := root.resolveMediaTrack("")
	if err != nil {
		t.Fatal(err)
	}
	if name != "1.m4s" {
		t.Fatalf("got %q want 1.m4s", name)
	}
	if got := root.initTrackName(name); got != "0.mp4" {
		t.Fatalf("init track %q want 0.mp4", got)
	}
}
