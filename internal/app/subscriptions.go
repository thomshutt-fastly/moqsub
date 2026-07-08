package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/thomshutt/quic/internal/moqt/draft18"
	"github.com/thomshutt/quic/internal/richlog"
)

type trackRole int

const (
	roleCatalog trackRole = iota
	roleInit
	roleMedia
)

type subscribeResult struct {
	alias uint64
	err   error
}

type trackSubscription struct {
	requestID uint64
	namespace string
	trackName string
	role      trackRole

	stream     *quic.Stream
	okCh       chan subscribeResult
	firstObjCh chan []byte
	gotFirst   bool
}

type subscriptionRegistry struct {
	mu        sync.Mutex
	byRequest map[uint64]*trackSubscription
	byAlias   map[uint64]*trackSubscription
	nextID    uint64
}

func newSubscriptionRegistry() *subscriptionRegistry {
	return &subscriptionRegistry{
		byRequest: make(map[uint64]*trackSubscription),
		byAlias:   make(map[uint64]*trackSubscription),
	}
}

func (reg *subscriptionRegistry) allocRequestID() uint64 {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	id := reg.nextID
	reg.nextID += 2
	return id
}

func (reg *subscriptionRegistry) register(sub *trackSubscription) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.byRequest[sub.requestID] = sub
}

func (reg *subscriptionRegistry) bindAlias(sub *trackSubscription, alias uint64) {
	reg.mu.Lock()
	reg.byAlias[alias] = sub
	reg.mu.Unlock()
}

func (role trackRole) String() string {
	switch role {
	case roleCatalog:
		return "catalog"
	case roleInit:
		return "init"
	case roleMedia:
		return "media"
	default:
		return fmt.Sprintf("unknown(%d)", int(role))
	}
}

func formatSubscribeRejectError(namespace, track string, role trackRole, reqErr draft18.RequestErrorMessage) error {
	return fmt.Errorf(
		"subscribe rejected namespace=%q track=%q role=%s code=%d (%s) retry=%d reason=%q",
		namespace,
		track,
		role,
		reqErr.Code,
		draft18.RequestErrorCodeName(reqErr.Code),
		reqErr.RetryInterval,
		reqErr.Reason,
	)
}


func (reg *subscriptionRegistry) lookupAlias(alias uint64) *trackSubscription {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return reg.byAlias[alias]
}

// lookupDelivery resolves a subgroup stream to a subscription. SUBSCRIBE_OK and
// subgroup data arrive on separate QUIC streams, so objects can show up before
// the alias map is populated. moq-rs draft-18 interop sets track_alias equal to
// request_id, so fall back to the request map when the alias is not yet known.
func (reg *subscriptionRegistry) lookupDelivery(trackAlias uint64) (*trackSubscription, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if sub := reg.byAlias[trackAlias]; sub != nil {
		return sub, false
	}
	if sub := reg.byRequest[trackAlias]; sub != nil {
		return sub, true
	}
	return nil, false
}

func (r *Runner) bootstrapFromCatalog(ctx context.Context) error {
	waitTimeout := 15 * time.Second

	catSub := r.newTrackSubscription(catalogTrackName, roleCatalog)
	if err := r.sendSubscribeMessage(ctx, catSub); err != nil {
		return err
	}
	if err := r.waitSubscribeOK(ctx, catSub, waitTimeout); err != nil {
		return fmt.Errorf("catalog subscribe: %w", err)
	}
	catPayload, err := r.waitFirstObject(ctx, catSub, waitTimeout)
	if err != nil {
		return fmt.Errorf("catalog object: %w", err)
	}
	root, err := parseCatalog(catPayload)
	if err != nil {
		return err
	}
	printCatalog(catPayload)
	r.log.Info("parsed catalog",
		"tracks", len(root.Tracks),
		"namespace", root.CommonTrackFields.Namespace)

	mediaTrack, err := root.resolveMediaTrack("")
	if err != nil {
		return err
	}
	initTrack := root.initTrackName(mediaTrack)
	r.log.Info("resolved tracks from catalog", "init", initTrack, "media", mediaTrack)
	r.rich.Add(richlog.Event{
		Dir:     richlog.DirLocal,
		Channel: "catalog",
		Name:    "CATALOG PARSED",
		Summary: fmt.Sprintf("%d track(s) · init %s · media %s", len(root.Tracks), initTrack, mediaTrack),
		Fields: []richlog.Field{
			{Key: "catalog_bytes", Value: fmt.Sprint(len(catPayload))},
			{Key: "namespace", Value: root.CommonTrackFields.Namespace},
			{Key: "packaging", Value: root.CommonTrackFields.Packaging},
			{Key: "init_track", Value: initTrack},
			{Key: "media_track", Value: mediaTrack},
		},
		Explain: "The first object on the .catalog track is a JSON document (MoQ catalog format) describing the broadcast: which media tracks exist, their codecs, and which init track pairs with each. The catalog itself is not part of the transport draft — it rides on a regular track with a well-known name.",
		Body:    formatCatalogJSON(catPayload),
	})

	initSub := r.newTrackSubscription(initTrack, roleInit)
	if err := r.sendSubscribeMessage(ctx, initSub); err != nil {
		return err
	}
	if err := r.waitSubscribeOK(ctx, initSub, waitTimeout); err != nil {
		return fmt.Errorf("init subscribe: %w", err)
	}
	initPayload, err := r.waitFirstObject(ctx, initSub, waitTimeout)
	if err != nil {
		return fmt.Errorf("init object: %w", err)
	}
	if err := r.writePayload(initPayload); err != nil {
		return fmt.Errorf("write init: %w", err)
	}
	atomic.AddUint64(&r.stats.bytesWritten, uint64(len(initPayload)))
	r.log.Info("wrote init segment", "track", initTrack, "bytes", len(initPayload))
	r.rich.Add(richlog.Event{
		Dir:     richlog.DirLocal,
		Channel: "output sink",
		Name:    "INIT SEGMENT WRITTEN",
		Summary: fmt.Sprintf("track %s · %d bytes to output", initTrack, len(initPayload)),
		Fields: []richlog.Field{
			{Key: "track", Value: initTrack},
			{Key: "bytes", Value: fmt.Sprint(len(initPayload))},
		},
		Explain: "The init track's single object holds the fMP4 initialization section (ftyp + moov: codec configuration, track metadata). It is written to the output pipe before any media fragment so the player can decode the moof+mdat fragments that follow.",
	})

	mediaSub := r.newTrackSubscription(mediaTrack, roleMedia)
	if err := r.sendSubscribeMessage(ctx, mediaSub); err != nil {
		return err
	}
	if err := r.waitSubscribeOK(ctx, mediaSub, waitTimeout); err != nil {
		return fmt.Errorf("media subscribe: %w", err)
	}
	r.log.Info("media subscription ready", "track", mediaTrack, "request_id", mediaSub.requestID)
	return nil
}

func (r *Runner) newTrackSubscription(trackName string, role trackRole) *trackSubscription {
	sub := &trackSubscription{
		requestID: r.subs.allocRequestID(),
		namespace: r.cfg.Namespace,
		trackName: trackName,
		role:      role,
		okCh:      make(chan subscribeResult, 1),
	}
	if role == roleCatalog || role == roleInit {
		sub.firstObjCh = make(chan []byte, 1)
	}
	r.subs.register(sub)
	return sub
}

func (r *Runner) sendSubscribeMessage(ctx context.Context, sub *trackSubscription) error {
	ns, err := r.trackNamespace()
	if err != nil {
		return err
	}
	payload, err := draft18.EncodeSubscribe(draft18.SubscribeMessage{
		RequestID:      sub.requestID,
		TrackNamespace: ns,
		TrackName:      []byte(sub.trackName),
	})
	if err != nil {
		return fmt.Errorf("encode subscribe: %w", err)
	}

	// draft-18 §3.3: SUBSCRIBE is the first message on a new bidirectional
	// "request stream". The peer replies with SUBSCRIBE_OK or REQUEST_ERROR
	// as a message on that same stream (§10.7-10.8).
	stream, err := r.conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open request stream: %w", err)
	}
	sub.stream = stream
	if err := draft18.WriteFrame(stream, draft18.MsgSubscribe, payload); err != nil {
		return fmt.Errorf("write subscribe: %w", err)
	}

	go r.readRequestStream(sub, stream)

	nsFields := splitNamespace(r.cfg.Namespace)
	r.log.Info("sent subscribe",
		"namespace", nsFields,
		"track", sub.trackName,
		"track_path", r.cfg.Namespace+"/"+sub.trackName,
		"request_id", sub.requestID,
		"stream_id", stream.StreamID(),
		"role", sub.role.String())
	r.rich.Add(richlog.Event{
		Dir:     richlog.DirSent,
		Channel: fmt.Sprintf("request stream %d", stream.StreamID()),
		Name:    "SUBSCRIBE",
		Summary: fmt.Sprintf("track %s/%s (%s) · request %d", r.cfg.Namespace, sub.trackName, sub.role, sub.requestID),
		Fields: []richlog.Field{
			{Key: "request_id", Value: fmt.Sprint(sub.requestID)},
			{Key: "track_namespace", Value: fmt.Sprintf("%v", nsFields)},
			{Key: "track_name", Value: sub.trackName},
			{Key: "role (local)", Value: sub.role.String()},
			{Key: "stream_id", Value: fmt.Sprint(stream.StreamID())},
		},
		Explain: "SUBSCRIBE asks the publisher to deliver a track's objects as they are produced. It must be the first message on a new bidirectional request stream; the response (SUBSCRIBE_OK or REQUEST_ERROR) comes back on this same stream. Request IDs are client-chosen and increase by two.",
		Spec:    richlog.SpecRef{Label: "SUBSCRIBE", Section: "10.7"},
	})
	return nil
}

// readRequestStream reads control messages sent by the peer on a bidirectional
// request stream. The response to SUBSCRIBE is exactly one SUBSCRIBE_OK or
// REQUEST_ERROR as the first message (draft-18 §3.4.1), followed by any later
// control messages for this subscription. The request is implied by the stream,
// so these messages carry no Request ID.
func (r *Runner) readRequestStream(sub *trackSubscription, stream *quic.Stream) {
	for {
		msgType, payload, err := draft18.ReadFrame(stream)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				r.log.Debug("request stream closed",
					"request_id", sub.requestID,
					"track", sub.trackName,
					"error", err)
			}
			return
		}
		switch msgType {
		case draft18.MsgSubscribeOK:
			ok, err := draft18.DecodeSubscribeOK(payload)
			if err != nil {
				r.log.Warn("decode subscribe_ok failed", "track", sub.trackName, "error", err)
				return
			}
			r.log.Info("received subscribe_ok",
				"request_id", sub.requestID,
				"track", sub.trackName,
				"track_alias", ok.TrackAlias,
				"num_parameters", ok.NumParams,
				"track_properties_bytes", len(ok.TrackProperties))
			r.rich.Add(richlog.Event{
				Dir:     richlog.DirRecv,
				Channel: fmt.Sprintf("request stream %d", stream.StreamID()),
				Name:    "SUBSCRIBE_OK",
				Summary: fmt.Sprintf("track %s accepted · alias %d", sub.trackName, ok.TrackAlias),
				Fields: []richlog.Field{
					{Key: "track (from request)", Value: sub.trackName},
					{Key: "track_alias", Value: fmt.Sprint(ok.TrackAlias)},
					{Key: "num_parameters", Value: fmt.Sprint(ok.NumParams)},
					{Key: "track_properties_bytes", Value: fmt.Sprint(len(ok.TrackProperties))},
				},
				Explain: "The publisher accepted the subscription. It assigns a track alias — a compact integer that stands in for the full namespace + track name on every subgroup stream that follows. No request ID appears on the wire: the request is implied by the stream this arrives on.",
				Spec:    richlog.SpecRef{Label: "SUBSCRIBE_OK / Track Alias", Section: "10.8"},
			})
			r.subs.bindAlias(sub, ok.TrackAlias)
			select {
			case sub.okCh <- subscribeResult{alias: ok.TrackAlias}:
			default:
			}
		case draft18.MsgRequestError:
			reqErr, err := draft18.DecodeRequestError(payload)
			if err != nil {
				r.log.Warn("decode request_error failed", "track", sub.trackName, "error", err)
				return
			}
			r.logSubscribeRejected(reqErr, sub)
			select {
			case sub.okCh <- subscribeResult{err: formatSubscribeRejectError(sub.namespace, sub.trackName, sub.role, reqErr)}:
			default:
			}
			return
		default:
			r.log.Debug("request stream control message",
				"request_id", sub.requestID,
				"track", sub.trackName,
				"type", fmt.Sprintf("0x%x", msgType),
				"payload_bytes", len(payload))
		}
	}
}

func (r *Runner) waitSubscribeOK(ctx context.Context, sub *trackSubscription, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-sub.okCh:
		if res.err != nil {
			return res.err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timeout waiting subscribe_ok for %q", sub.trackName)
	}
}

func (r *Runner) waitFirstObject(ctx context.Context, sub *trackSubscription, timeout time.Duration) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case payload := <-sub.firstObjCh:
		return payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting first object for %q", sub.trackName)
	}
}

func (r *Runner) deliverSubgroupObject(sub *trackSubscription, payload []byte) error {
	switch sub.role {
	case roleCatalog, roleInit:
		if !sub.gotFirst {
			sub.gotFirst = true
			select {
			case sub.firstObjCh <- payload:
			default:
			}
		}
		return nil
	case roleMedia:
		r.recordFirstObject()
		if err := r.writePayload(payload); err != nil {
			return err
		}
		atomic.AddUint64(&r.stats.bytesWritten, uint64(len(payload)))
		return nil
	default:
		return nil
	}
}

func (r *Runner) trackNamespace() ([][]byte, error) {
	nsFields := splitNamespace(r.cfg.Namespace)
	return draft18.EncodeTrackNamespaceFromStrings(nsFields)
}
