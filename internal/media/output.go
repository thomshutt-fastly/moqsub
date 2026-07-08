package media

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

type Mode string

const (
	ModeStdout  Mode = "stdout"
	ModeFFmpeg  Mode = "ffmpeg"
	ModeFFplay  Mode = "ffplay"
	ModeCommand Mode = "command"
)

type Config struct {
	Mode      Mode
	FFmpegCmd string
	FFplayCmd string
	PipeCmd   string
}

type Sink struct {
	mode Mode
	w    io.WriteCloser

	cmd   *exec.Cmd
	wait  sync.Once
	waitE error
}

func NewSink(ctx context.Context, cfg Config) (*Sink, error) {
	switch cfg.Mode {
	case ModeStdout:
		return &Sink{mode: cfg.Mode, w: nopCloser{Writer: os.Stdout}}, nil
	case ModeFFmpeg:
		return startCommandSink(ctx, cfg.Mode, choose(cfg.FFmpegCmd, "ffmpeg -hide_banner -loglevel warning -i pipe:0 -f null -"))
	case ModeFFplay:
		return startCommandSink(ctx, cfg.Mode, choose(cfg.FFplayCmd, "ffplay -hide_banner -fflags nobuffer -flags low_delay -framedrop -i pipe:0"))
	case ModeCommand:
		if cfg.PipeCmd == "" {
			return nil, fmt.Errorf("pipe command is required when mode=command")
		}
		return startCommandSink(ctx, cfg.Mode, cfg.PipeCmd)
	default:
		return nil, fmt.Errorf("unknown output mode: %q", cfg.Mode)
	}
}

func (s *Sink) Write(p []byte) (int, error) {
	return s.w.Write(p)
}

func (s *Sink) Close() error {
	if s == nil || s.w == nil {
		return nil
	}
	if err := s.w.Close(); err != nil {
		return err
	}
	if s.cmd != nil {
		s.wait.Do(func() {
			s.waitE = s.cmd.Wait()
		})
		return s.waitE
	}
	return nil
}

func startCommandSink(ctx context.Context, mode Mode, command string) (*Sink, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Sink{
		mode: mode,
		w:    stdin,
		cmd:  cmd,
	}, nil
}

func choose(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

type nopCloser struct {
	io.Writer
}

func (n nopCloser) Close() error {
	return nil
}
