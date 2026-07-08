package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/thomshutt/quic/internal/media"
	"github.com/thomshutt/quic/internal/moqt/draft18"
	"github.com/thomshutt/quic/internal/quicclient"
	"github.com/thomshutt/quic/internal/richlog"
)

type Config struct {
	RelayURI string

	Namespace string

	Output media.Config

	Client quicclient.Config

	// RichLogPath, when set, writes an explorable single-file HTML log of
	// every MoQ message exchanged during the session.
	RichLogPath string
}

type Runner struct {
	cfg Config
	log *slog.Logger

	conn *quic.Conn

	controlStream *quic.SendStream

	outMu sync.Mutex
	sink  *media.Sink

	stats Stats

	peerSetupOnce sync.Once
	peerSetupSeen chan struct{}

	subs *subscriptionRegistry
	rich *richlog.Recorder
}

type Stats struct {
	startedAt time.Time

	bytesWritten  uint64
	objectsSeen   uint64
	groupsSeen    uint64
	gapHints      uint64
	statusObjects uint64
	streamResets  uint64

	firstObjectNanos int64
}

func New(cfg Config, log *slog.Logger) *Runner {
	r := &Runner{
		cfg:           cfg,
		log:           log,
		peerSetupSeen: make(chan struct{}),
		subs:          newSubscriptionRegistry(),
	}
	if cfg.RichLogPath != "" {
		r.rich = richlog.New()
	}
	return r
}

func (r *Runner) Run(ctx context.Context) error {
	r.stats.startedAt = time.Now()

	r.rich.SetMeta("relay", r.cfg.RelayURI)
	r.rich.SetMeta("namespace", r.cfg.Namespace)
	r.rich.SetMeta("output", string(r.cfg.Output.Mode))
	defer func() {
		if err := r.rich.WriteHTML(r.cfg.RichLogPath); err != nil {
			r.log.Error("rich log write failed", "error", err)
		} else if r.rich != nil {
			r.log.Info("rich log written", "path", r.cfg.RichLogPath)
		}
	}()

	client := quicclient.New(r.cfg.Client, r.log)
	conn, relayURL, err := client.Dial(ctx)
	if err != nil {
		return err
	}
	r.conn = conn
	r.rich.Add(richlog.Event{
		Dir:     richlog.DirLocal,
		Channel: "quic connection",
		Name:    "CONNECTED",
		Summary: fmt.Sprintf("QUIC connection to %s established", conn.RemoteAddr()),
		Fields: []richlog.Field{
			{Key: "remote", Value: conn.RemoteAddr().String()},
			{Key: "alpn", Value: r.cfg.Client.ALPN},
		},
		Explain: "MoQ Transport runs directly over a raw QUIC connection here (not WebTransport). The TLS handshake negotiated the moqt-18 ALPN token, so both endpoints agree on the draft-18 wire format before any MoQ message is sent.",
		Spec:    richlog.SpecRef{Label: "Session initialization", Section: "3.3"},
	})
	defer conn.CloseWithError(0, "moqsub shutdown")
	defer func() {
		if r.controlStream != nil {
			_ = r.controlStream.Close()
		}
	}()

	r.sink, err = media.NewSink(ctx, r.cfg.Output)
	if err != nil {
		return fmt.Errorf("output sink: %w", err)
	}
	defer func() {
		if err := r.sink.Close(); err != nil {
			r.log.Error("output close failed", "error", err)
		}
	}()

	errCh := make(chan error, 8)

	go r.acceptUniStreams(ctx, errCh)
	go r.acceptPeerBidiStreams(ctx, errCh)

	if err := r.sendClientSetup(relayURL); err != nil {
		return err
	}

	select {
	case <-r.peerSetupSeen:
		r.log.Info("peer setup received")
	case <-time.After(5 * time.Second):
		r.log.Warn("peer setup not observed within timeout; continuing")
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := r.bootstrapFromCatalog(ctx); err != nil {
		return err
	}

	r.log.Info("subscription established, receiving objects")
	for {
		select {
		case <-ctx.Done():
			r.printSummary()
			return nil
		case err := <-errCh:
			if errors.Is(err, context.Canceled) {
				r.printSummary()
				return nil
			}
			var streamErr *quic.StreamError
			if errors.As(err, &streamErr) {
				atomic.AddUint64(&r.stats.streamResets, 1)
				r.log.Warn("stream reset", "code", uint64(streamErr.ErrorCode), "remote", streamErr.Remote)
				continue
			}
			r.printSummary()
			return err
		}
	}
}

func (r *Runner) sendClientSetup(relayURL *url.URL) error {
	stream, err := r.conn.OpenUniStreamSync(context.Background())
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	r.controlStream = stream

	path := relayURL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if relayURL.RawQuery != "" {
		path += "?" + relayURL.RawQuery
	}
	authority := relayURL.Host
	options := []draft18.SetupOption{
		{Type: draft18.SetupOptionAuthority, Raw: []byte(authority)},
		{Type: draft18.SetupOptionImplementation, Raw: []byte("moqsub-go/0.1.0")},
	}
	// Match moq-rs: omit PATH when it is "/" so both ends land in the unscoped bucket.
	if path != "/" {
		options = append([]draft18.SetupOption{{Type: draft18.SetupOptionPath, Raw: []byte(path)}}, options...)
	}
	payload, err := draft18.EncodeSetup(draft18.SetupMessage{Options: options})
	if err != nil {
		return fmt.Errorf("encode setup: %w", err)
	}

	// The control stream begins directly with the SETUP message; its message
	// type (0x2F00) doubles as the unidirectional stream type.
	if err := draft18.WriteFrame(stream, draft18.MsgSetup, payload); err != nil {
		return fmt.Errorf("write setup frame: %w", err)
	}
	r.log.Info("sent setup", "authority", authority, "path", path)
	r.rich.Add(richlog.Event{
		Dir:     richlog.DirSent,
		Channel: "control stream",
		Name:    "SETUP",
		Summary: fmt.Sprintf("authority=%s path=%s", authority, path),
		Fields: []richlog.Field{
			{Key: "stream_id", Value: fmt.Sprint(stream.StreamID())},
			{Key: "AUTHORITY (0x05)", Value: authority},
			{Key: "PATH (0x01)", Value: path + " (omitted on wire when \"/\")"},
			{Key: "IMPLEMENTATION (0x07)", Value: "moqsub-go/0.1.0"},
		},
		Explain: "Each peer opens one unidirectional control stream that begins with a SETUP message; the stream-type varint 0x2F00 doubles as the message type. SETUP carries key-value setup parameters that scope the session (authority, path, implementation identifier).",
		Spec:    richlog.SpecRef{Label: "SETUP", Section: "10.3"},
	})
	return nil
}

func (r *Runner) acceptUniStreams(ctx context.Context, errCh chan<- error) {
	for {
		stream, err := r.conn.AcceptUniStream(ctx)
		if err != nil {
			errCh <- err
			return
		}
		go func(s *quic.ReceiveStream) {
			if err := r.handleUniStream(s); err != nil && !errors.Is(err, io.EOF) {
				errCh <- err
			}
		}(stream)
	}
}

func (r *Runner) acceptPeerBidiStreams(ctx context.Context, errCh chan<- error) {
	for {
		stream, err := r.conn.AcceptStream(ctx)
		if err != nil {
			errCh <- err
			return
		}
		go func(s *quic.Stream) {
			defer s.Close()
			msgType, payload, err := draft18.ReadFrame(s)
			if err != nil && !errors.Is(err, io.EOF) {
				r.log.Warn("peer bidi stream read failed", "stream_id", s.StreamID(), "error", err)
				return
			}
			r.log.Info("peer bidi stream message",
				"stream_id", s.StreamID(),
				"type", fmt.Sprintf("0x%x", msgType),
				"len", len(payload))
		}(stream)
	}
}

func (r *Runner) handleUniStream(stream *quic.ReceiveStream) error {
	streamType, err := draft18.ReadVarint(stream)
	if err != nil {
		return fmt.Errorf("read uni stream type: %w", err)
	}
	r.log.Debug("uni stream accepted", "stream_id", stream.StreamID(), "stream_type", fmt.Sprintf("0x%x", streamType))

	switch {
	case streamType == draft18.StreamTypeSetup:
		return r.handleControlStream(stream, streamType)
	case draft18.IsSubgroupStreamType(streamType):
		return r.handleSubgroupStream(stream, streamType)
	case streamType == draft18.StreamTypeFetchHeader:
		r.log.Debug("received fetch stream (not handled)", "stream_id", stream.StreamID())
		return nil
	default:
		r.log.Warn("unknown unidirectional stream type",
			"stream_id", stream.StreamID(),
			"stream_type", fmt.Sprintf("0x%x", streamType))
		return nil
	}
}

func (r *Runner) handleControlStream(stream *quic.ReceiveStream, firstMsgType uint64) error {
	// The unidirectional stream type varint we already consumed is also the
	// message type of the first control message (SETUP). Read its body first,
	// then loop over any subsequent full control messages.
	firstBody, err := draft18.ReadFrameBody(stream)
	if err != nil {
		return fmt.Errorf("read first control message body: %w", err)
	}
	if err := r.dispatchControlMessage(firstMsgType, firstBody); err != nil {
		return err
	}

	for {
		msgType, payload, err := draft18.ReadFrame(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("control stream read: %w", err)
		}
		if err := r.dispatchControlMessage(msgType, payload); err != nil {
			return err
		}
	}
}

func (r *Runner) dispatchControlMessage(msgType uint64, payload []byte) error {
	switch msgType {
	case draft18.MsgSetup:
		setup, err := draft18.DecodeSetup(payload)
		if err != nil {
			return fmt.Errorf("decode setup: %w", err)
		}
		r.log.Info("received setup", "options", len(setup.Options))
		fields := make([]richlog.Field, 0, len(setup.Options)+1)
		fields = append(fields, richlog.Field{Key: "num_options", Value: fmt.Sprint(len(setup.Options))})
		for _, opt := range setup.Options {
			fields = append(fields, richlog.Field{
				Key:   fmt.Sprintf("option 0x%02x", opt.Type),
				Value: fmt.Sprintf("%q", opt.Raw),
			})
		}
		r.rich.Add(richlog.Event{
			Dir:     richlog.DirRecv,
			Channel: "control stream",
			Name:    "SETUP",
			Summary: fmt.Sprintf("relay setup with %d option(s) — session established", len(setup.Options)),
			Fields:  fields,
			Explain: "The relay's own control stream mirrors ours: it begins with the relay's SETUP. Once both SETUPs are exchanged the MoQ session is established and requests can flow.",
			Spec:    richlog.SpecRef{Label: "SETUP", Section: "10.3"},
		})
		r.peerSetupOnce.Do(func() { close(r.peerSetupSeen) })
	case draft18.MsgGoAway:
		r.log.Warn("received goaway", "payload_bytes", len(payload))
		r.rich.Add(richlog.Event{
			Dir:     richlog.DirRecv,
			Channel: "control stream",
			Name:    "GOAWAY",
			Summary: fmt.Sprintf("%d payload bytes", len(payload)),
			Explain: "The relay asks us to migrate to a new session, typically ahead of maintenance or shutdown.",
			Spec:    richlog.SpecRef{Label: "GOAWAY", Section: "10.4"},
		})
	default:
		r.log.Debug("control message",
			"type", fmt.Sprintf("0x%x", msgType),
			"payload_bytes", len(payload))
	}
	return nil
}

func (r *Runner) handleSubgroupStream(stream *quic.ReceiveStream, streamType uint64) error {
	header, err := draft18.ParseSubgroupHeader(stream, streamType)
	if err != nil {
		return fmt.Errorf("parse subgroup header: %w", err)
	}
	r.log.Debug("subgroup header",
		"stream_id", stream.StreamID(),
		"stream_type", fmt.Sprintf("0x%x", header.Type),
		"track_alias", header.TrackAlias,
		"group_id", header.GroupID,
		"has_subgroup_id", header.HasSubgroupID,
		"subgroup_id", header.SubgroupID,
		"has_extensions", header.HasExtensions,
		"end_of_group", header.EndOfGroup,
		"priority", header.PublisherPriority)
	atomic.AddUint64(&r.stats.groupsSeen, 1)

	sub, aliasPending := r.subs.lookupDelivery(header.TrackAlias)
	if aliasPending {
		r.log.Debug("object arrived before subscribe_ok; matched by request_id",
			"track_alias", header.TrackAlias,
			"request_id", sub.requestID,
			"track", sub.trackName)
	}

	trackName := "(unknown)"
	if sub != nil {
		trackName = sub.trackName
	}
	r.rich.Add(richlog.Event{
		Dir:     richlog.DirRecv,
		Channel: fmt.Sprintf("subgroup stream %d", stream.StreamID()),
		Name:    "SUBGROUP_HEADER",
		Summary: fmt.Sprintf("track %s · alias %d · group %d · priority %d", trackName, header.TrackAlias, header.GroupID, header.PublisherPriority),
		Fields: []richlog.Field{
			{Key: "stream_id", Value: fmt.Sprint(stream.StreamID())},
			{Key: "stream_type", Value: fmt.Sprintf("0x%x", header.Type)},
			{Key: "track_alias", Value: fmt.Sprint(header.TrackAlias)},
			{Key: "group_id", Value: fmt.Sprint(header.GroupID)},
			{Key: "subgroup_id", Value: fmt.Sprint(header.SubgroupID)},
			{Key: "has_extensions", Value: fmt.Sprint(header.HasExtensions)},
			{Key: "end_of_group", Value: fmt.Sprint(header.EndOfGroup)},
			{Key: "publisher_priority", Value: fmt.Sprint(header.PublisherPriority)},
		},
		Explain: "The publisher opens a new unidirectional stream per subgroup. Its header binds the stream to a track (via the compact track alias from SUBSCRIBE_OK) and a group, then a sequence of objects follows on the same stream. Stream type 0x15 means the subgroup ID is explicit and objects may carry extension headers.",
		Spec:    richlog.SpecRef{Label: "Subgroup Header", Section: "11.4.2"},
	})

	reader := draft18.NewSubgroupObjectReader(stream, header)
	for {
		obj, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		atomic.AddUint64(&r.stats.objectsSeen, 1)
		if obj.ObjectIDDelta > 0 {
			atomic.AddUint64(&r.stats.gapHints, 1)
		}
		if obj.IsStatusObject {
			atomic.AddUint64(&r.stats.statusObjects, 1)
			r.log.Debug("status object",
				"stream_id", stream.StreamID(),
				"group_id", header.GroupID,
				"object_id", obj.ObjectID,
				"status", obj.ObjectStatus)
			continue
		}
		if sub == nil {
			r.log.Debug("object for unmapped track alias",
				"track_alias", header.TrackAlias,
				"payload_bytes", len(obj.Payload))
			continue
		}
		if err := r.deliverSubgroupObject(sub, obj.Payload); err != nil {
			return fmt.Errorf("deliver object: %w", err)
		}
		r.rich.AddObject(int64(stream.StreamID()), sub.trackName, header.TrackAlias, header.GroupID, obj.ObjectID, len(obj.Payload))
	}
}

func (r *Runner) logSubscribeRejected(reqErr draft18.RequestErrorMessage, sub *trackSubscription) {
	attrs := []any{
		"code", reqErr.Code,
		"code_name", draft18.RequestErrorCodeName(reqErr.Code),
		"retry_interval", reqErr.RetryInterval,
		"reason", reqErr.Reason,
	}
	if sub != nil {
		attrs = append(attrs,
			"request_id", sub.requestID,
			"namespace", sub.namespace,
			"track", sub.trackName,
			"track_path", sub.namespace+"/"+sub.trackName,
			"role", sub.role.String(),
		)
	} else {
		attrs = append(attrs,
			"namespace", r.cfg.Namespace,
		)
	}
	if len(reqErr.Redirect) > 0 {
		attrs = append(attrs, "redirect_bytes", len(reqErr.Redirect))
	}
	if reqErr.Code == 0x10 {
		attrs = append(attrs,
			"hint", "track or namespace not published on relay — is moq-pub running with matching --name?")
	}
	r.log.Error("subscribe rejected", attrs...)

	ev := richlog.Event{
		Dir:     richlog.DirRecv,
		Name:    "REQUEST_ERROR",
		Summary: fmt.Sprintf("code %d (%s): %s", reqErr.Code, draft18.RequestErrorCodeName(reqErr.Code), reqErr.Reason),
		Fields: []richlog.Field{
			{Key: "error_code", Value: fmt.Sprintf("%d (%s)", reqErr.Code, draft18.RequestErrorCodeName(reqErr.Code))},
			{Key: "retry_interval", Value: fmt.Sprint(reqErr.RetryInterval)},
			{Key: "reason", Value: reqErr.Reason},
		},
		Explain: "The publisher rejected the request. Exactly one SUBSCRIBE_OK or REQUEST_ERROR is sent in response to a SUBSCRIBE, on the same request stream; the request is implied by the stream so no request ID appears on the wire. DOES_NOT_EXIST (0x10) means no publisher is serving this namespace/track.",
		Spec:    richlog.SpecRef{Label: "REQUEST_ERROR", Section: "10.6"},
	}
	if sub != nil {
		ev.Channel = fmt.Sprintf("request stream (request %d)", sub.requestID)
		ev.Fields = append(ev.Fields,
			richlog.Field{Key: "track", Value: sub.namespace + "/" + sub.trackName},
			richlog.Field{Key: "role", Value: sub.role.String()},
		)
	}
	r.rich.Add(ev)
}

func (r *Runner) writePayload(payload []byte) error {
	r.outMu.Lock()
	defer r.outMu.Unlock()
	_, err := r.sink.Write(payload)
	return err
}

func (r *Runner) recordFirstObject() {
	if atomic.LoadInt64(&r.stats.firstObjectNanos) != 0 {
		return
	}
	atomic.CompareAndSwapInt64(&r.stats.firstObjectNanos, 0, time.Since(r.stats.startedAt).Nanoseconds())
}

func (r *Runner) printSummary() {
	firstObjNs := atomic.LoadInt64(&r.stats.firstObjectNanos)
	var firstObjMs int64
	if firstObjNs > 0 {
		firstObjMs = firstObjNs / int64(time.Millisecond)
	}
	r.log.Info("summary",
		"duration_s", time.Since(r.stats.startedAt).Round(time.Millisecond).Seconds(),
		"bytes_written", atomic.LoadUint64(&r.stats.bytesWritten),
		"objects_seen", atomic.LoadUint64(&r.stats.objectsSeen),
		"groups_seen", atomic.LoadUint64(&r.stats.groupsSeen),
		"status_objects", atomic.LoadUint64(&r.stats.statusObjects),
		"object_id_gap_hints", atomic.LoadUint64(&r.stats.gapHints),
		"stream_resets", atomic.LoadUint64(&r.stats.streamResets),
		"first_object_latency_ms", firstObjMs)
}

func splitNamespace(namespace string) []string {
	trimmed := strings.Trim(namespace, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
