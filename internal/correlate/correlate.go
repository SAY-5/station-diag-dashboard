// Package correlate groups individual actuator failures into incidents.
//
// A test-station fault usually shows up as a cascade: an actuator draws too
// much current, the rail it shares sags a few hundred milliseconds later,
// and an encoder downstream drops out. The rule engine flags each of those
// as a separate Failure. Treated separately they look like three unrelated
// problems; treated together they are one incident with a probable root
// cause (the earliest subsystem in the cascade).
//
// The Correlator collapses failures that land within a sliding time window
// of one another, for the same run, into a single Incident. The window is
// configurable. Incidents are ordered by the timestamp of their failures so
// the earliest-in-window subsystem is presented first as the probable root
// cause.
package correlate

import (
	"sort"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/rules"
)

// DefaultWindow is the correlation window used when none is configured.
// 500ms comfortably spans an overcurrent-to-rail-sag-to-encoder cascade
// without merging genuinely independent faults minutes apart.
const DefaultWindow = 500 * time.Millisecond

// Member is one failure inside a correlated incident, carrying just the
// fields the timeline view needs.
type Member struct {
	RuleID    string    `json:"rule_id"`
	Subsystem string    `json:"subsystem"`
	Actuator  string    `json:"actuator_id"`
	Severity  string    `json:"severity"`
	Detail    string    `json:"detail"`
	At        time.Time `json:"at"`
}

// Incident is a group of failures that occurred close together in time for
// one run. Members are ordered earliest first, so Members[0] names the
// probable root-cause subsystem. RootCause repeats that subsystem for
// callers that do not want to index into Members.
type Incident struct {
	ID        string    `json:"id"`
	StationID string    `json:"station_id"`
	RunID     string    `json:"run_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	RootCause string    `json:"root_cause"`
	Members   []Member  `json:"members"`
}

// Subsystems returns the distinct subsystems in the incident, ordered by
// first appearance: the probable root-cause ordering.
func (in Incident) Subsystems() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range in.Members {
		if _, ok := seen[m.Subsystem]; ok {
			continue
		}
		seen[m.Subsystem] = struct{}{}
		out = append(out, m.Subsystem)
	}
	return out
}

// Correlator accumulates failures and groups them into incidents. It is not
// safe for concurrent use; the pipeline owns one Correlator and feeds it
// from a single goroutine under its own lock.
type Correlator struct {
	window time.Duration
	// open tracks the still-growing incident per run. A failure extends the
	// open incident when it lands within window of the incident's last
	// member; otherwise the open incident is closed and a new one starts.
	open map[string]*Incident
	seq  int64
}

// New returns a Correlator with the given window. A non-positive window
// falls back to DefaultWindow.
func New(window time.Duration) *Correlator {
	if window <= 0 {
		window = DefaultWindow
	}
	return &Correlator{window: window, open: map[string]*Incident{}}
}

// Window reports the configured correlation window.
func (c *Correlator) Window() time.Duration { return c.window }

// Observe feeds one failure into the correlator. It returns the incident
// the failure belongs to, and whether that incident is newly created. The
// returned incident is a snapshot: it keeps growing internally as long as
// it stays open, so callers that need the final form should also watch for
// the next Observe that closes it, or call Flush.
//
// A returned incident reflects every failure seen so far in the group,
// including the one just observed, with members ordered earliest first.
func (c *Correlator) Observe(f rules.Failure) (Incident, bool) {
	cur := c.open[f.RunID]
	m := memberOf(f)

	// A failure joins the open incident when it is within window of that
	// incident's most recent member. time.Sub is signed; an out-of-order
	// failure (negative gap) still joins as long as it is within window.
	if cur != nil {
		gap := f.At.Sub(cur.EndedAt)
		if gap < 0 {
			gap = -gap
		}
		if gap <= c.window {
			cur.Members = append(cur.Members, m)
			sortMembers(cur.Members)
			cur.StartedAt = cur.Members[0].At
			if f.At.After(cur.EndedAt) {
				cur.EndedAt = f.At
			}
			cur.RootCause = cur.Members[0].Subsystem
			return *cur, false
		}
	}

	// Start a fresh incident; any previously open one for this run is now
	// closed simply by being replaced.
	c.seq++
	inc := &Incident{
		ID:        incidentID(f.RunID, c.seq),
		StationID: f.StationID,
		RunID:     f.RunID,
		StartedAt: f.At,
		EndedAt:   f.At,
		RootCause: f.SubsystemOrActuator(),
		Members:   []Member{m},
	}
	c.open[f.RunID] = inc
	return *inc, true
}

// Flush returns every still-open incident and clears the correlator's
// state. Call it when ingestion stops to surface a trailing cascade.
func (c *Correlator) Flush() []Incident {
	out := make([]Incident, 0, len(c.open))
	for _, inc := range c.open {
		out = append(out, *inc)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	c.open = map[string]*Incident{}
	return out
}

func memberOf(f rules.Failure) Member {
	return Member{
		RuleID:    f.RuleID,
		Subsystem: f.SubsystemOrActuator(),
		Actuator:  f.Actuator,
		Severity:  f.Severity,
		Detail:    f.Detail,
		At:        f.At,
	}
}

// sortMembers orders members earliest first, breaking ties by rule id so
// the ordering is deterministic for incidents built from the same failures
// in any arrival order.
func sortMembers(ms []Member) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].At.Equal(ms[j].At) {
			return ms[i].RuleID < ms[j].RuleID
		}
		return ms[i].At.Before(ms[j].At)
	})
}

func incidentID(runID string, seq int64) string {
	return runID + "#" + itoa(seq)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [21]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
