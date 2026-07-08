# moqsub (MoQ Subscriber Utility)

`moqsub` is a learning-focused Go CLI for subscribing to Media over QUIC (MoQT) tracks over raw QUIC, with protocol-level visibility.

## Goals

- Hand-rolled draft-18 oriented subscriber behavior over `quic-go`.
- Rich protocol debugging across setup, request, and data channels.
- Pipe received object payloads to stdout, ffmpeg, or ffplay.
- Subscriber-only scope: no publisher and no relay implementation.

## Current Scope

Implemented:

- Raw QUIC session dialing with configurable ALPN (default `moqt-18`).
- Client `SETUP` control stream send + peer control stream parsing.
- `SUBSCRIBE` request stream send + `SUBSCRIBE_OK` / `REQUEST_ERROR` handling.
- Incoming unidirectional stream dispatch:
  - control stream (`SETUP`, `GOAWAY`, generic control message logging)
  - subgroup data stream parsing (`SUBGROUP_HEADER` + subgroup object fields)
- Object payload forwarding to configured output sink.
- Runtime summary counters (bytes, objects, groups, gap hints, resets, first-object latency).

Not implemented yet:

- WebTransport.
- Fetch and datagram object handling.
- Draft compatibility shims for draft-14/16.
- Publishing functionality.

## Build

```bash
go build ./cmd/moqsub
```

## Usage

Basic run (stdout payload):

```bash
./moqsub \
  --relay moqt://localhost:4443 \
  --namespace anon/bbb \
  --output stdout
```

Pipe payload to ffplay:

```bash
./moqsub \
  --relay moqt://localhost:4443 \
  --namespace anon/bbb \
  --output ffplay
```

## Flags

- `--relay`: `moqt://` URI for raw QUIC relay endpoint.
- `--namespace`: Slash-delimited track namespace fields.
- `--alpn`: ALPN protocol, default `moqt-18`.
- `--insecure`: Skip TLS cert verification (local/testing only).
- `--output`: `stdout|ffmpeg|ffplay|command`.
- `--ffmpeg-cmd`: Command for `--output=ffmpeg`.
- `--ffplay-cmd`: Command for `--output=ffplay`.
- `--pipe-cmd`: Command for `--output=command`.
- `--log-format`: `text|json`.
- `--rich-log`: Write an explorable single-file HTML log of every MoQ message exchanged (with field breakdowns, explanations, and spec links) to the given path.

## Relay Profiles (Test Targets)

These endpoints change over time; treat as examples and verify current availability.

- Draft-18 interop target:
  - `moqt://draft-18-interop.cloudflare.mediaoverquic.com:443`
# moqsub
