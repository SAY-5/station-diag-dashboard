package unit

import (
	"math/rand"
	"testing"

	"github.com/SAY-5/station-diag-dashboard/internal/hub"
)

// Property test for the hub last_seq reconnect cursor.
//
// A virtual client connects, drains some messages, disconnects, then
// reconnects with the highest seq it has seen. Across a random interleaving
// of publishes, connects and disconnects the invariant is:
//
//   - the client never misses an event (every seq in (lastSeen, maxSeq] that
//     was published while a backlog slot still retained it is delivered), and
//   - the client never receives the same seq twice.
//
// The hub keeps a bounded backlog, so an event can legitimately be missed if
// the client stays disconnected long enough for it to age out. The test sizes
// the backlog larger than the maximum publish burst so no legitimate drop can
// occur, which makes "never misses" a hard invariant.

func TestPropertyHubCursorNeverMissesNeverDuplicates(t *testing.T) {
	const iterations = 200

	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter) + 1))

		// Backlog capacity comfortably exceeds the largest possible burst
		// (steps * maxBurst) so no event ages out before the client resumes.
		const steps = 40
		const maxBurst = 5
		h := hub.New(steps*maxBurst + 16)

		var lastSeen int64
		seen := map[int64]bool{}
		var maxPublished int64

		var sub *hub.Subscriber
		connect := func() {
			sub = h.Subscribe(lastSeen)
		}
		drain := func() {
			if sub == nil {
				return
			}
			for {
				select {
				case m, ok := <-sub.C():
					if !ok {
						return
					}
					if seen[m.Seq] {
						t.Fatalf("iter %d: duplicate delivery of seq %d", iter, m.Seq)
					}
					seen[m.Seq] = true
					if m.Seq > lastSeen {
						lastSeen = m.Seq
					}
				default:
					return
				}
			}
		}
		disconnect := func() {
			if sub != nil {
				drain()
				h.Unsubscribe(sub)
				sub = nil
			}
		}

		connect()
		for step := 0; step < steps; step++ {
			switch rng.Intn(3) {
			case 0: // publish a burst
				burst := rng.Intn(maxBurst) + 1
				for b := 0; b < burst; b++ {
					h.Publish(sampleEvent("event"))
					maxPublished++
				}
			case 1: // drain whatever is buffered
				drain()
			default: // reconnect cycle
				disconnect()
				connect()
			}
		}
		// Final settle: drain everything still in flight.
		drain()
		disconnect()

		// Invariant: every published seq was delivered exactly once.
		if int64(len(seen)) != maxPublished {
			t.Fatalf("iter %d: delivered %d distinct seqs, published %d",
				iter, len(seen), maxPublished)
		}
		for s := int64(1); s <= maxPublished; s++ {
			if !seen[s] {
				t.Fatalf("iter %d: missed seq %d (published %d)", iter, s, maxPublished)
			}
		}
	}
}

// TestPropertyHubBacklogBoundHolds checks that across random burst sizes the
// retained backlog never exceeds the configured capacity and always keeps
// the newest messages.
func TestPropertyHubBacklogBoundHolds(t *testing.T) {
	const iterations = 200
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter) + 1))
		capacity := rng.Intn(20) + 1
		h := hub.New(capacity)

		total := rng.Intn(200)
		for i := 0; i < total; i++ {
			h.Publish(sampleEvent("event"))
		}
		backlog := h.Backlog(0)
		want := total
		if want > capacity {
			want = capacity
		}
		if len(backlog) != want {
			t.Fatalf("iter %d: backlog len %d want %d (cap %d total %d)",
				iter, len(backlog), want, capacity, total)
		}
		// Backlog must be the newest contiguous suffix and strictly ordered.
		for i := 1; i < len(backlog); i++ {
			if backlog[i].Seq != backlog[i-1].Seq+1 {
				t.Fatalf("iter %d: backlog not contiguous at %d", iter, i)
			}
		}
		if total > 0 && backlog[len(backlog)-1].Seq != int64(total) {
			t.Fatalf("iter %d: backlog tail seq %d want %d",
				iter, backlog[len(backlog)-1].Seq, total)
		}
	}
}
