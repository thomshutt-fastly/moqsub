package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const catalogTrackName = ".catalog"

// catalogRoot is the MoQ catalog JSON published on the ".catalog" track (format v1).
type catalogRoot struct {
	Version              uint16              `json:"version"`
	CommonTrackFields    catalogCommonFields `json:"commonTrackFields"`
	Tracks               []catalogTrack      `json:"tracks"`
}

type catalogCommonFields struct {
	Namespace string `json:"namespace,omitempty"`
	Packaging string `json:"packaging,omitempty"`
}

type catalogTrack struct {
	Name            string              `json:"name"`
	InitTrack       string              `json:"initTrack,omitempty"`
	SelectionParams catalogSelectionParam `json:"selectionParams"`
}

type catalogSelectionParam struct {
	Codec  string `json:"codec,omitempty"`
	Width  uint32 `json:"width,omitempty"`
	Height uint32 `json:"height,omitempty"`
}

func parseCatalog(data []byte) (catalogRoot, error) {
	var root catalogRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return catalogRoot{}, fmt.Errorf("parse catalog json: %w", err)
	}
	if root.Version != 1 {
		return catalogRoot{}, fmt.Errorf("unsupported catalog version: %d", root.Version)
	}
	return root, nil
}

func formatCatalogJSON(data []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data)
	}
	return buf.String()
}

func printCatalog(data []byte) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "── catalog (%d bytes) ──\n", len(data))
	fmt.Fprintln(os.Stderr, formatCatalogJSON(data))
	fmt.Fprintln(os.Stderr, "── end catalog ──")
	fmt.Fprintln(os.Stderr)
}

func (r catalogRoot) initTrackName(mediaTrack string) string {
	if mediaTrack != "" {
		for _, t := range r.Tracks {
			if t.Name == mediaTrack && t.InitTrack != "" {
				return t.InitTrack
			}
		}
	}
	for _, t := range r.Tracks {
		if t.InitTrack != "" {
			return t.InitTrack
		}
	}
	return "0.mp4"
}

func (r catalogRoot) resolveMediaTrack(preferred string) (string, error) {
	if preferred != "" {
		if len(r.Tracks) == 0 {
			return preferred, nil
		}
		for _, t := range r.Tracks {
			if t.Name == preferred {
				return preferred, nil
			}
		}
		return "", fmt.Errorf("track %q not found in catalog", preferred)
	}
	for _, t := range r.Tracks {
		if t.Name == catalogTrackName {
			continue
		}
		if strings.HasPrefix(t.SelectionParams.Codec, "avc1") || strings.HasPrefix(t.SelectionParams.Codec, "hvc1") {
			return t.Name, nil
		}
	}
	for _, t := range r.Tracks {
		if t.Name != catalogTrackName {
			return t.Name, nil
		}
	}
	return "", fmt.Errorf("catalog contains no media tracks")
}
