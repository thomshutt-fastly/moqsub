package debuglog

import (
	"log/slog"
	"os"
)

type Config struct {
	Format string
}

func New(cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	// This is a learning tool: keep the output focused on the messages, so
	// shorten the timestamp to HH:MM:SS and drop the level entirely.
	opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) > 0 {
			return a
		}
		switch a.Key {
		case slog.TimeKey:
			return slog.String(slog.TimeKey, a.Value.Time().Format("15:04:05"))
		case slog.LevelKey:
			return slog.Attr{}
		}
		return a
	}
	var handler slog.Handler
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}
