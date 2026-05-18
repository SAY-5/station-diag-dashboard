// Package e2e holds the gated end-to-end test. It builds the dashboard and
// station-emitter binaries, runs them against each other, connects a
// WebSocket client and asserts that an injected actuator failure surfaces
// as a failure_highlight message.
//
// The test is gated: it only runs when RUN_E2E=1 is set. CI runs it in a
// dedicated job. It is hermetic and needs no real hardware.
package e2e

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsMessage mirrors the hub.Message wire format. Only the fields the test
// asserts on are decoded.
type wsMessage struct {
	Seq     int64  `json:"seq"`
	Kind    string `json:"kind"`
	Failure *struct {
		RuleID    string `json:"rule_id"`
		StationID string `json:"station_id"`
		RunID     string `json:"run_id"`
		Detail    string `json:"detail"`
		Severity  string `json:"severity"`
	} `json:"failure,omitempty"`
}

func TestEndToEndFailureHighlight(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("e2e test gated: set RUN_E2E=1 to run")
	}

	deadline := time.Now().Add(90 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	repoRoot := repoRoot(t)
	binDir := t.TempDir()

	dashboardBin := buildBinary(t, ctx, repoRoot, "./cmd/dashboard", binDir, "dashboard")
	emitterBin := buildBinary(t, ctx, repoRoot, "./cmd/station-emitter", binDir, "emitter")

	httpAddr := freeAddr(t)
	tcpAddr := freeAddr(t)
	dbPath := filepath.Join(binDir, "e2e.db")

	dashboard := exec.CommandContext(ctx, dashboardBin,
		"-http", httpAddr, "-tcp", tcpAddr, "-db", dbPath)
	dashboard.Dir = repoRoot
	dashboard.Stdout = os.Stderr
	dashboard.Stderr = os.Stderr
	if err := dashboard.Start(); err != nil {
		t.Fatalf("start dashboard: %v", err)
	}
	defer stopProcess(dashboard)

	waitForListen(t, ctx, httpAddr)
	waitForListen(t, ctx, tcpAddr)

	// Connect the WebSocket client before the emitter starts so no
	// failure_highlight is missed; last_seq=0 also backfills any early ones.
	wsURL := "ws://" + httpAddr + "/ws?last_seq=0"
	conn := dialWS(t, ctx, wsURL)
	defer func() { _ = conn.Close() }()

	// A fixed seed guarantees the emitter injects at least one failing run
	// so the rule engine has a signature to flag.
	emitter := exec.CommandContext(ctx, emitterBin,
		"-target", tcpAddr, "-runs", "12", "-interval", "60ms", "-seed", "20260518")
	emitter.Stdout = os.Stderr
	emitter.Stderr = os.Stderr
	if err := emitter.Start(); err != nil {
		t.Fatalf("start emitter: %v", err)
	}
	defer stopProcess(emitter)

	got := waitForFailureHighlight(t, ctx, conn)
	if got.Failure == nil {
		t.Fatal("failure_highlight message had no failure payload")
	}
	if got.Failure.RuleID == "" {
		t.Fatal("failure_highlight failure had empty rule_id")
	}
	if got.Failure.RunID == "" {
		t.Fatal("failure_highlight failure had empty run_id")
	}
	t.Logf("received failure_highlight: rule=%s run=%s detail=%q",
		got.Failure.RuleID, got.Failure.RunID, got.Failure.Detail)
}

// waitForFailureHighlight reads hub messages until a failure_highlight
// arrives or the context deadline is reached.
func waitForFailureHighlight(t *testing.T, ctx context.Context, conn *websocket.Conn) wsMessage {
	t.Helper()
	for {
		dl, ok := ctx.Deadline()
		if !ok {
			dl = time.Now().Add(60 * time.Second)
		}
		if err := conn.SetReadDeadline(dl); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message before failure_highlight: %v", err)
		}
		var msg wsMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("decode websocket message: %v", err)
		}
		if msg.Kind == "failure_highlight" {
			return msg
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// The test runs from tests/e2e; the repo root is two levels up.
	return filepath.Dir(filepath.Dir(wd))
}

func buildBinary(t *testing.T, ctx context.Context, repoRoot, pkg, outDir, name string) string {
	t.Helper()
	out := filepath.Join(outDir, name)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, pkg)
	cmd.Dir = repoRoot
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", pkg, err)
	}
	return out
}

// freeAddr reserves a free loopback port by binding and immediately
// releasing it, then returns the address for a child process to claim.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// waitForListen polls until a TCP dial to addr succeeds or ctx expires.
func waitForListen(t *testing.T, ctx context.Context, addr string) {
	t.Helper()
	for {
		if ctx.Err() != nil {
			t.Fatalf("timed out waiting for %s to listen", addr)
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s to listen", addr)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func dialWS(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	for {
		if ctx.Err() != nil {
			t.Fatalf("timed out dialing %s", url)
		}
		conn, _, err := dialer.DialContext(ctx, url, nil)
		if err == nil {
			return conn
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out dialing %s: %v", url, err)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// stopProcess terminates a child process and reaps it.
func stopProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
