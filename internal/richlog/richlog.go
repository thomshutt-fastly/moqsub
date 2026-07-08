// Package richlog records the MoQ messages exchanged during a session and
// renders them as a single self-contained HTML page for exploration. It is a
// learning aid: every event carries its decoded fields, a short explanation,
// and a link to the relevant section of draft-ietf-moq-transport-18.
package richlog

import (
	"fmt"
	"sync"
	"time"
)

const specBase = "https://datatracker.ietf.org/doc/html/draft-ietf-moq-transport-18"

// Direction of an event relative to the subscriber.
type Direction string

const (
	DirSent  Direction = "sent"
	DirRecv  Direction = "received"
	DirLocal Direction = "local"
)

// Field is one decoded wire field (or context value) on an event.
type Field struct {
	Key   string
	Value string
}

// SpecRef links an event to the draft-18 text.
type SpecRef struct {
	Label   string
	Section string // e.g. "10.7"
}

func (s SpecRef) URL() string {
	if s.Section == "" {
		return ""
	}
	return specBase + "#section-" + s.Section
}

// Event is a single protocol message or notable session moment.
type Event struct {
	Time    time.Time
	Dir     Direction
	Channel string // e.g. "control stream", "request stream 0", "subgroup stream 15"
	Name    string // e.g. "SUBSCRIBE"
	Summary string // one-line human summary
	Fields  []Field
	Explain string  // explanatory paragraph
	Spec    SpecRef // optional spec link
	Body    string  // optional preformatted payload (e.g. catalog JSON)

	// objects is non-nil for the synthetic "object group" event that
	// aggregates media object deliveries per subgroup stream + group.
	objects *objectGroup
}

type objectRow struct {
	Time     time.Time
	ObjectID uint64
	Bytes    int
}

type objectGroup struct {
	StreamID   int64
	Track      string
	TrackAlias uint64
	GroupID    uint64
	Rows       []objectRow
	TotalBytes int64
}

// Recorder accumulates events. All methods are safe for concurrent use and
// no-ops on a nil receiver, so call sites never need guards.
type Recorder struct {
	mu     sync.Mutex
	meta   map[string]string
	events []*Event
	groups map[string]*objectGroup
	start  time.Time
}

func New() *Recorder {
	return &Recorder{
		meta:   make(map[string]string),
		groups: make(map[string]*objectGroup),
		start:  time.Now(),
	}
}

// SetMeta records session-level metadata shown in the page header.
func (r *Recorder) SetMeta(key, value string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.meta[key] = value
}

// Add appends an event, stamping the time if unset.
func (r *Recorder) Add(ev Event) {
	if r == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, &ev)
}

// AddObject records one media object delivery. Objects are aggregated into a
// collapsible group per (subgroup stream, group ID) rather than one event each.
func (r *Recorder) AddObject(streamID int64, track string, trackAlias, groupID, objectID uint64, payloadBytes int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%d/%d", streamID, groupID)
	grp, ok := r.groups[key]
	if !ok {
		grp = &objectGroup{
			StreamID:   streamID,
			Track:      track,
			TrackAlias: trackAlias,
			GroupID:    groupID,
		}
		r.groups[key] = grp
		r.events = append(r.events, &Event{
			Time:    time.Now(),
			Dir:     DirRecv,
			Channel: fmt.Sprintf("subgroup stream %d", streamID),
			Name:    "OBJECTS",
			Explain: "Media objects delivered on this subgroup stream. Each object is one payload (here, a CMAF moof+mdat fragment or part of one). Object IDs are delta-encoded on the wire and increase within the subgroup.",
			Spec:    SpecRef{Label: "Objects", Section: "11.2"},
			objects: grp,
		})
	}
	grp.Rows = append(grp.Rows, objectRow{Time: time.Now(), ObjectID: objectID, Bytes: payloadBytes})
	grp.TotalBytes += int64(payloadBytes)
}
