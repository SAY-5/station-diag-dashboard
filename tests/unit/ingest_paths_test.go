package unit

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
)

// collector is an ingest.Sink that records every published event.
type collector struct {
	mu     sync.Mutex
	events []ingest.LogEvent
}

func (c *collector) Publish(e ingest.LogEvent) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *collector) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func waitFor(t *testing.T, want int, get func() int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if get() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events, have %d", want, get())
}

func TestTCPListenerIngestsAndSkipsMalformed(t *testing.T) {
	sink := &collector{}
	addr := freeAddr(t)
	listener := ingest.NewTCPListener(addr, sink, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = listener.Run(ctx) }()

	conn := dialWithRetry(t, addr)
	defer func() { _ = conn.Close() }()

	lines := []string{
		`{"station_id":"s1","run_id":"r1","level":"info","subsystem":"actuator","message":"move_command"}`,
		`not json at all`,
		``,
		`{"station_id":"s1","run_id":"r1","level":"warn","subsystem":"actuator","message":"settling"}`,
	}
	for _, l := range lines {
		if _, err := conn.Write([]byte(l + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Only the two valid lines should arrive; malformed and blank are dropped.
	waitFor(t, 2, sink.len)
	time.Sleep(100 * time.Millisecond)
	if got := sink.len(); got != 2 {
		t.Fatalf("expected 2 valid events, got %d", got)
	}
}

func TestFileTailIngests(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "station.log")
	if err := os.WriteFile(path, []byte(
		`{"station_id":"s1","run_id":"r1","level":"info","subsystem":"actuator","message":"first"}`+"\n"),
		0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	sink := &collector{}
	tail := ingest.NewFileTail(path, sink, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = tail.Run(ctx) }()

	waitFor(t, 1, sink.len)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_, _ = f.WriteString(`{"station_id":"s1","run_id":"r1","level":"warn","subsystem":"actuator","message":"second"}` + "\n")
	_ = f.Close()

	waitFor(t, 2, sink.len)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free addr: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func dialWithRetry(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			return conn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("could not dial %s", addr)
	return nil
}
