// Package hub implements the WebSocket fan-out hub. It assigns monotonic
// sequence numbers to ingested events, keeps a bounded backlog for
// backfill-on-connect, and fans every message out to all subscribers.
package hub

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/SAY-5/station-diag-dashboard/internal/correlate"
	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// MessageKind tags a hub message for the dashboard client.
type MessageKind string

const (
	// KindLogEvent carries a normalized LogEvent.
	KindLogEvent MessageKind = "log_event"
	// KindFailureHighlight carries a detected actuator failure.
	KindFailureHighlight MessageKind = "failure_highlight"
	// KindNoteAdded carries an operator note attached to a run.
	KindNoteAdded MessageKind = "note_added"
	// KindIncident carries a correlated group of failures with a probable
	// root-cause ordering.
	KindIncident MessageKind = "incident"
)

// Note is the payload for a KindNoteAdded message.
type Note struct {
	ID        int64  `json:"id"`
	StationID string `json:"station_id"`
	RunID     string `json:"run_id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// Message is a single hub message with a monotonic sequence number.
type Message struct {
	Seq      int64               `json:"seq"`
	Kind     MessageKind         `json:"kind"`
	Event    *ingest.LogEvent    `json:"event,omitempty"`
	Failure  *rules.Failure      `json:"failure,omitempty"`
	Note     *Note               `json:"note,omitempty"`
	Incident *correlate.Incident `json:"incident,omitempty"`
}

// Subscriber receives a copy of every message published after it joins,
// plus a backfill of recent messages on connect.
type Subscriber struct {
	ch     chan Message
	hub    *Hub
	closed bool
}

// C returns the subscriber's receive channel.
func (s *Subscriber) C() <-chan Message { return s.ch }

// Hub fans messages out to all subscribers and retains a bounded backlog.
type Hub struct {
	mu       sync.Mutex
	seq      int64
	backlog  []Message
	capacity int
	subs     map[*Subscriber]struct{}
	closed   bool
}

// New constructs a hub that retains the last backlogCapacity messages.
func New(backlogCapacity int) *Hub {
	if backlogCapacity < 1 {
		backlogCapacity = 1
	}
	return &Hub{
		capacity: backlogCapacity,
		subs:     map[*Subscriber]struct{}{},
	}
}

// nextLocked assigns and returns the next sequence number. Caller holds mu.
func (h *Hub) nextLocked() int64 {
	h.seq++
	return h.seq
}

// Publish is the ingest.Sink entry point: it wraps a LogEvent and fans out.
func (h *Hub) Publish(ev ingest.LogEvent) {
	h.broadcast(Message{Kind: KindLogEvent, Event: &ev})
}

// PublishFailure fans a detected failure out to all subscribers.
func (h *Hub) PublishFailure(f rules.Failure) {
	h.broadcast(Message{Kind: KindFailureHighlight, Failure: &f})
}

// PublishIncident fans a correlated incident out to all subscribers. The
// pipeline calls this each time an incident is created or extended, so a
// client always sees the latest grouping for an incident id.
func (h *Hub) PublishIncident(in correlate.Incident) {
	h.broadcast(Message{Kind: KindIncident, Incident: &in})
}

// PublishNote fans an operator note out to all subscribers.
func (h *Hub) PublishNote(n Note) {
	h.broadcast(Message{Kind: KindNoteAdded, Note: &n})
}

func (h *Hub) broadcast(m Message) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	m.Seq = h.nextLocked()
	if m.Event != nil {
		m.Event.Seq = m.Seq
	}
	h.backlog = append(h.backlog, m)
	if len(h.backlog) > h.capacity {
		h.backlog = h.backlog[len(h.backlog)-h.capacity:]
	}
	targets := make([]*Subscriber, 0, len(h.subs))
	for s := range h.subs {
		targets = append(targets, s)
	}
	h.mu.Unlock()

	for _, s := range targets {
		select {
		case s.ch <- m:
		default:
			// Slow subscriber: drop it rather than block the hub. The
			// client reconnects with last_seq and backfills the gap.
			h.Unsubscribe(s)
		}
	}
}

// Subscribe registers a subscriber and backfills every retained message with
// Seq greater than lastSeq. Pass lastSeq = 0 for a fresh connection.
func (h *Hub) Subscribe(lastSeq int64) *Subscriber {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Subscriber{ch: make(chan Message, 256), hub: h}
	if h.closed {
		s.closed = true
		close(s.ch)
		return s
	}
	for _, m := range h.backlog {
		if m.Seq > lastSeq {
			select {
			case s.ch <- m:
			default:
			}
		}
	}
	h.subs[s] = struct{}{}
	return s
}

// Unsubscribe removes a subscriber and closes its channel exactly once.
func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; !ok {
		return
	}
	delete(h.subs, s)
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// Backlog returns a copy of the retained messages with Seq above lastSeq.
func (h *Hub) Backlog(lastSeq int64) []Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Message
	for _, m := range h.backlog {
		if m.Seq > lastSeq {
			out = append(out, m)
		}
	}
	return out
}

// Shutdown drains and closes every subscriber, then refuses new traffic.
// It returns when ctx is done or all subscribers are released.
func (h *Hub) Shutdown(ctx context.Context) {
	h.mu.Lock()
	h.closed = true
	subs := make([]*Subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.subs = map[*Subscriber]struct{}{}
	h.mu.Unlock()

	done := make(chan struct{})
	go func() {
		for _, s := range subs {
			if !s.closed {
				s.closed = true
				close(s.ch)
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Encode marshals a message to JSON for transmission over the socket.
func Encode(m Message) ([]byte, error) { return json.Marshal(m) }
