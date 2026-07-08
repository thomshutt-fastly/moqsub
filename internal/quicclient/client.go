package quicclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"

	"github.com/quic-go/quic-go"
)

const Draft18ALPN = "moqt-18"

type Config struct {
	RelayURI         string
	InsecureSkipTLS  bool
	ALPN             string
	HandshakeTimeout time.Duration
}

type Client struct {
	cfg Config
	log *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Client {
	return &Client{cfg: cfg, log: log}
}

func (c *Client) Dial(ctx context.Context) (*quic.Conn, *url.URL, error) {
	relayURL, err := url.Parse(c.cfg.RelayURI)
	if err != nil {
		return nil, nil, fmt.Errorf("parse relay uri: %w", err)
	}
	if relayURL.Scheme != "moqt" {
		return nil, nil, fmt.Errorf("unsupported relay scheme %q (expected moqt)", relayURL.Scheme)
	}
	host := relayURL.Hostname()
	port := relayURL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	tlsConf := &tls.Config{
		NextProtos:         []string{choose(c.cfg.ALPN, Draft18ALPN)},
		ServerName:         host,
		InsecureSkipVerify: c.cfg.InsecureSkipTLS,
		MinVersion:         tls.VersionTLS13,
	}

	qconf := &quic.Config{
		EnableDatagrams: true,
	}

	dialCtx := ctx
	if c.cfg.HandshakeTimeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, c.cfg.HandshakeTimeout)
		defer cancel()
	}

	c.log.Info("dialing relay", "address", addr, "alpn", tlsConf.NextProtos[0])
	conn, err := quic.DialAddr(dialCtx, addr, tlsConf, qconf)
	if err != nil {
		return nil, nil, fmt.Errorf("quic dial: %w", err)
	}
	state := conn.ConnectionState().TLS
	c.log.Info("connected",
		"remote", conn.RemoteAddr().String(),
		"negotiated_protocol", state.NegotiatedProtocol,
		"tls_version", state.Version)
	return conn, relayURL, nil
}

func choose(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
