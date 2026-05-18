package ingest

import (
	"bufio"
	"context"
	"log/slog"
	"net"
)

// Sink receives normalized LogEvents from an ingestion path.
type Sink interface {
	Publish(LogEvent)
}

// TCPListener accepts test-station connections and reads newline-delimited
// JSON log lines, normalizing each into a LogEvent.
type TCPListener struct {
	addr   string
	sink   Sink
	logger *slog.Logger
}

// NewTCPListener constructs a listener bound to addr (e.g. ":7000").
func NewTCPListener(addr string, sink Sink, logger *slog.Logger) *TCPListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &TCPListener{addr: addr, sink: sink, logger: logger}
}

// Run listens until ctx is cancelled. It returns the listen error, if any,
// or nil on a clean context-driven shutdown.
func (l *TCPListener) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", l.addr)
	if err != nil {
		return err
	}
	l.logger.Info("tcp ingest listening", "addr", l.addr)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			l.logger.Warn("tcp accept failed", "error", err)
			continue
		}
		go l.handle(ctx, conn)
	}
}

func (l *TCPListener) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	remote := conn.RemoteAddr().String()
	l.logger.Info("station connected", "remote", remote)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		ev, err := ParseLine(scanner.Bytes())
		if err != nil {
			if err != ErrEmptyLine {
				l.logger.Warn("dropping malformed line", "remote", remote, "error", err)
			}
			continue
		}
		l.sink.Publish(ev)
	}
	if err := scanner.Err(); err != nil {
		l.logger.Warn("station read error", "remote", remote, "error", err)
	}
	l.logger.Info("station disconnected", "remote", remote)
}
