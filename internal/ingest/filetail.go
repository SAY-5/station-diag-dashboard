package ingest

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"
)

// FileTail follows a log file, emitting a LogEvent for each newline-delimited
// JSON line. It reads existing content first, then polls for appended lines,
// which keeps it portable across Windows and Linux without inotify.
type FileTail struct {
	path     string
	sink     Sink
	logger   *slog.Logger
	interval time.Duration
}

// NewFileTail constructs a tailer for path.
func NewFileTail(path string, sink Sink, logger *slog.Logger) *FileTail {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileTail{path: path, sink: sink, logger: logger, interval: 250 * time.Millisecond}
}

// Run tails the file until ctx is cancelled.
func (t *FileTail) Run(ctx context.Context) error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	t.logger.Info("file ingest tailing", "path", t.path)

	reader := bufio.NewReader(f)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		if err := t.drain(reader); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (t *FileTail) drain(reader *bufio.Reader) error {
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			ev, perr := ParseLine(line)
			if perr != nil {
				if perr != ErrEmptyLine {
					t.logger.Warn("dropping malformed line", "path", t.path, "error", perr)
				}
			} else {
				t.sink.Publish(ev)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
