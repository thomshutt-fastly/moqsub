package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thomshutt/quic/internal/app"
	"github.com/thomshutt/quic/internal/debuglog"
	"github.com/thomshutt/quic/internal/media"
	"github.com/thomshutt/quic/internal/quicclient"
)

func main() {
	cfg, logCfg, err := parseFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "flag error: %v\n", err)
		os.Exit(2)
	}
	log := debuglog.New(logCfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	r := app.New(cfg, log)
	if err := r.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		log.Error("moqsub failed", "error", err)
		os.Exit(1)
	}
}

func parseFlags() (app.Config, debuglog.Config, error) {
	var (
		relayURI   = flag.String("relay", "moqt://localhost:4443", "Relay URI with moqt:// scheme")
		namespace  = flag.String("namespace", "anon/bbb", "Track namespace as slash-delimited fields")
		insecure   = flag.Bool("insecure", false, "Skip TLS certificate verification")
		alpn       = flag.String("alpn", quicclient.Draft18ALPN, "ALPN value for raw QUIC")
		outputMode = flag.String("output", string(media.ModeStdout), "Output mode: stdout|ffmpeg|ffplay|command")

		ffmpegCmd = flag.String("ffmpeg-cmd", "", "Command used when --output=ffmpeg")
		ffplayCmd = flag.String("ffplay-cmd", "", "Command used when --output=ffplay")
		pipeCmd   = flag.String("pipe-cmd", "", "Command used when --output=command")

		logFormat = flag.String("log-format", "text", "Log format: text|json")
		richLog   = flag.String("rich-log", "", "Write an explorable HTML log of the session to this path (e.g. session.html)")

		timeout = flag.Duration("handshake-timeout", 10*time.Second, "Handshake timeout")
	)
	flag.Parse()

	switch media.Mode(*outputMode) {
	case media.ModeStdout, media.ModeFFmpeg, media.ModeFFplay, media.ModeCommand:
	default:
		return app.Config{}, debuglog.Config{}, fmt.Errorf("invalid output mode: %s", *outputMode)
	}

	return app.Config{
			RelayURI:  *relayURI,
			Namespace: *namespace,
			Output: media.Config{
				Mode:      media.Mode(*outputMode),
				FFmpegCmd: *ffmpegCmd,
				FFplayCmd: *ffplayCmd,
				PipeCmd:   *pipeCmd,
			},
			Client: quicclient.Config{
				RelayURI:         *relayURI,
				InsecureSkipTLS:  *insecure,
				ALPN:             *alpn,
				HandshakeTimeout: *timeout,
			},
			RichLogPath: *richLog,
		}, debuglog.Config{
			Format: *logFormat,
		}, nil
}
