package unit

import (
	"context"
	"testing"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

func sampleEvent(msg string) ingest.LogEvent {
	return ingest.LogEvent{
		TS: time.Now().UTC(), StationID: "s1", RunID: "r1",
		Level: "info", Subsystem: "actuator", Message: msg,
	}
}

func recvWithin(t *testing.T, c <-chan hub.Message, d time.Duration) hub.Message {
	t.Helper()
	select {
	case m, ok := <-c:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return m
	case <-time.After(d):
		t.Fatal("timed out waiting for hub message")
		return hub.Message{}
	}
}

func TestHubFanOutToMultipleSubscribers(t *testing.T) {
	h := hub.New(100)
	a := h.Subscribe(0)
	b := h.Subscribe(0)
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	h.Publish(sampleEvent("move_command"))

	ma := recvWithin(t, a.C(), time.Second)
	mb := recvWithin(t, b.C(), time.Second)
	if ma.Seq != 1 || mb.Seq != 1 {
		t.Fatalf("both subscribers should see seq 1, got %d and %d", ma.Seq, mb.Seq)
	}
	if ma.Event == nil || ma.Event.Seq != 1 {
		t.Fatalf("event seq not stamped: %+v", ma.Event)
	}
}

func TestHubMonotonicSequence(t *testing.T) {
	h := hub.New(100)
	s := h.Subscribe(0)
	defer h.Unsubscribe(s)

	h.Publish(sampleEvent("a"))
	h.PublishFailure(rules.Failure{RuleID: "actuator_timeout", RunID: "r1"})
	h.PublishNote(hub.Note{ID: 1, RunID: "r1", Body: "checking"})

	var seqs []int64
	for i := 0; i < 3; i++ {
		seqs = append(seqs, recvWithin(t, s.C(), time.Second).Seq)
	}
	if seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Fatalf("sequence not monotonic: %v", seqs)
	}
}

func TestHubBackfillOnConnect(t *testing.T) {
	h := hub.New(100)
	for i := 0; i < 5; i++ {
		h.Publish(sampleEvent("event"))
	}
	// A fresh subscriber receives the full backlog.
	s := h.Subscribe(0)
	defer h.Unsubscribe(s)
	for i := 0; i < 5; i++ {
		m := recvWithin(t, s.C(), time.Second)
		if m.Seq != int64(i+1) {
			t.Fatalf("backfill out of order: want %d got %d", i+1, m.Seq)
		}
	}
}

func TestHubLastSeqCursorResumesWithoutGaps(t *testing.T) {
	h := hub.New(100)
	for i := 0; i < 6; i++ {
		h.Publish(sampleEvent("event"))
	}
	// Client reconnects having already seen up to seq 4.
	s := h.Subscribe(4)
	defer h.Unsubscribe(s)
	m1 := recvWithin(t, s.C(), time.Second)
	m2 := recvWithin(t, s.C(), time.Second)
	if m1.Seq != 5 || m2.Seq != 6 {
		t.Fatalf("cursor resume gave %d,%d want 5,6", m1.Seq, m2.Seq)
	}
	select {
	case extra := <-s.C():
		t.Fatalf("unexpected extra message seq %d", extra.Seq)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHubBacklogIsBounded(t *testing.T) {
	h := hub.New(3)
	for i := 0; i < 10; i++ {
		h.Publish(sampleEvent("event"))
	}
	backlog := h.Backlog(0)
	if len(backlog) != 3 {
		t.Fatalf("backlog should be capped at 3, got %d", len(backlog))
	}
	if backlog[0].Seq != 8 || backlog[2].Seq != 10 {
		t.Fatalf("backlog should retain newest, got %d..%d", backlog[0].Seq, backlog[2].Seq)
	}
}

func TestHubShutdownClosesSubscribers(t *testing.T) {
	h := hub.New(10)
	s := h.Subscribe(0)
	h.Publish(sampleEvent("event"))
	_ = recvWithin(t, s.C(), time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h.Shutdown(ctx)

	// Draining a closed subscriber eventually yields a closed channel.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-s.C():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("subscriber channel not closed after shutdown")
		}
	}
}

func TestHubPublishAfterShutdownIsNoop(t *testing.T) {
	h := hub.New(10)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h.Shutdown(ctx)
	h.Publish(sampleEvent("late")) // must not panic
	if len(h.Backlog(0)) != 0 {
		t.Fatal("shutdown hub should not accept new events")
	}
}
