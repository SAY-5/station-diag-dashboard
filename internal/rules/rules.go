// Package rules implements the YAML-driven actuator failure rule engine.
// The engine is pure: Evaluate takes an ordered slice of events and returns
// the failures detected, with no side effects.
package rules

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"gopkg.in/yaml.v3"
)

// Kind selects which detector a rule uses.
type Kind string

const (
	// KindTimeout fires when a trigger event has no resolve event in window.
	KindTimeout Kind = "timeout"
	// KindThreshold fires when a numeric field crosses a bound.
	KindThreshold Kind = "threshold"
	// KindConsecutive fires on N triggers with no resolve in between.
	KindConsecutive Kind = "consecutive"
)

// Rule is a single actuator failure signature.
type Rule struct {
	ID          string `yaml:"id"`
	Kind        Kind   `yaml:"kind"`
	Description string `yaml:"description"`
	Subsystem   string `yaml:"subsystem"`
	Severity    string `yaml:"severity"`

	// Level optionally scopes a rule to a single log level.
	Level string `yaml:"level"`

	// Timeout and consecutive rule fields.
	TriggerPattern string        `yaml:"trigger_pattern"`
	ResolvePattern string        `yaml:"resolve_pattern"`
	Within         time.Duration `yaml:"within"`
	Count          int           `yaml:"count"`

	// Threshold rule fields.
	Field     string  `yaml:"field"`
	Operator  string  `yaml:"operator"`
	Threshold float64 `yaml:"threshold"`
}

// Failure is a detected rule match against a specific run.
type Failure struct {
	RuleID    string    `json:"rule_id"`
	StationID string    `json:"station_id"`
	RunID     string    `json:"run_id"`
	Subsystem string    `json:"subsystem"`
	Actuator  string    `json:"actuator_id"`
	Detail    string    `json:"detail"`
	Severity  string    `json:"severity"`
	At        time.Time `json:"at"`
}

// SubsystemOrActuator returns the failure's subsystem, falling back to the
// actuator id when the originating rule declared no subsystem. Correlation
// groups by this value, so it must never be empty for a real failure.
func (f Failure) SubsystemOrActuator() string {
	if f.Subsystem != "" {
		return f.Subsystem
	}
	if f.Actuator != "" {
		return f.Actuator
	}
	return "unknown"
}

// Engine holds a validated set of rules.
type Engine struct {
	rules []Rule
}

type ruleFile struct {
	Rules []Rule `yaml:"rules"`
}

// Load parses and validates a YAML rule document.
func Load(data []byte) (*Engine, error) {
	var rf ruleFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("rules: parse yaml: %w", err)
	}
	if len(rf.Rules) == 0 {
		return nil, errors.New("rules: no rules defined")
	}
	for i := range rf.Rules {
		if err := validate(rf.Rules[i]); err != nil {
			return nil, err
		}
	}
	return &Engine{rules: rf.Rules}, nil
}

func validate(r Rule) error {
	if r.ID == "" {
		return errors.New("rules: rule missing id")
	}
	switch r.Kind {
	case KindTimeout:
		if r.TriggerPattern == "" || r.ResolvePattern == "" {
			return fmt.Errorf("rules: %s: timeout needs trigger_pattern and resolve_pattern", r.ID)
		}
		if r.Within <= 0 {
			return fmt.Errorf("rules: %s: timeout needs a positive within", r.ID)
		}
	case KindThreshold:
		if r.Field == "" {
			return fmt.Errorf("rules: %s: threshold needs field", r.ID)
		}
		if r.Operator != "gt" && r.Operator != "lt" {
			return fmt.Errorf("rules: %s: operator must be gt or lt", r.ID)
		}
	case KindConsecutive:
		if r.TriggerPattern == "" || r.ResolvePattern == "" {
			return fmt.Errorf("rules: %s: consecutive needs trigger_pattern and resolve_pattern", r.ID)
		}
		if r.Count < 2 {
			return fmt.Errorf("rules: %s: consecutive needs count >= 2", r.ID)
		}
	default:
		return fmt.Errorf("rules: %s: unknown kind %q", r.ID, r.Kind)
	}
	return nil
}

// Rules returns the loaded rules.
func (e *Engine) Rules() []Rule { return e.rules }

// Evaluate runs every rule over events and returns all failures detected.
// events must be ordered by arrival; the engine groups by (run, actuator).
func (e *Engine) Evaluate(events []ingest.LogEvent) []Failure {
	var out []Failure
	for _, r := range e.rules {
		switch r.Kind {
		case KindTimeout:
			out = append(out, evalTimeout(r, events)...)
		case KindThreshold:
			out = append(out, evalThreshold(r, events)...)
		case KindConsecutive:
			out = append(out, evalConsecutive(r, events)...)
		}
	}
	return out
}

func scoped(r Rule, e ingest.LogEvent) bool {
	if r.Subsystem != "" && e.Subsystem != r.Subsystem {
		return false
	}
	if r.Level != "" && e.Level != r.Level {
		return false
	}
	return true
}

func matches(pattern, message string) bool {
	return strings.Contains(strings.ToLower(message), strings.ToLower(pattern))
}

type key struct {
	run      string
	actuator string
}

func evalTimeout(r Rule, events []ingest.LogEvent) []Failure {
	type pending struct {
		station string
		at      time.Time
	}
	open := map[key]pending{}
	var out []Failure
	for _, e := range events {
		if !scoped(r, e) {
			continue
		}
		k := key{run: e.RunID, actuator: e.ActuatorID}
		switch {
		case matches(r.TriggerPattern, e.Message):
			open[k] = pending{station: e.StationID, at: e.TS}
		case matches(r.ResolvePattern, e.Message):
			if p, ok := open[k]; ok {
				if e.TS.Sub(p.at) > r.Within {
					out = append(out, failure(r, e.StationID, e.RunID, e.ActuatorID,
						fmt.Sprintf("resolve arrived %s after trigger, window is %s",
							e.TS.Sub(p.at).Round(time.Millisecond), r.Within), e.TS))
				}
				delete(open, k)
			}
		}
	}
	for k, p := range open {
		out = append(out, failure(r, p.station, k.run, k.actuator,
			fmt.Sprintf("no %q seen after %q", r.ResolvePattern, r.TriggerPattern), p.at))
	}
	return out
}

func evalThreshold(r Rule, events []ingest.LogEvent) []Failure {
	var out []Failure
	for _, e := range events {
		if !scoped(r, e) {
			continue
		}
		v, ok := e.Field(r.Field)
		if !ok {
			continue
		}
		crossed := (r.Operator == "gt" && v > r.Threshold) ||
			(r.Operator == "lt" && v < r.Threshold)
		if crossed {
			out = append(out, failure(r, e.StationID, e.RunID, e.ActuatorID,
				fmt.Sprintf("%s=%g crossed %s bound %g", r.Field, v, r.Operator, r.Threshold), e.TS))
		}
	}
	return out
}

func evalConsecutive(r Rule, events []ingest.LogEvent) []Failure {
	type streak struct {
		station string
		count   int
		first   time.Time
	}
	open := map[key]streak{}
	var out []Failure
	for _, e := range events {
		if !scoped(r, e) {
			continue
		}
		k := key{run: e.RunID, actuator: e.ActuatorID}
		switch {
		case matches(r.TriggerPattern, e.Message):
			s := open[k]
			if s.count == 0 {
				s.first = e.TS
			}
			s.station = e.StationID
			s.count++
			open[k] = s
			if s.count >= r.Count {
				out = append(out, failure(r, e.StationID, e.RunID, e.ActuatorID,
					fmt.Sprintf("%d consecutive %q with no %q", s.count, r.TriggerPattern, r.ResolvePattern), e.TS))
				delete(open, k)
			}
		case matches(r.ResolvePattern, e.Message):
			delete(open, k)
		}
	}
	return out
}

func failure(r Rule, station, run, actuator, detail string, at time.Time) Failure {
	sev := r.Severity
	if sev == "" {
		sev = "error"
	}
	return Failure{
		RuleID:    r.ID,
		StationID: station,
		RunID:     run,
		Subsystem: r.Subsystem,
		Actuator:  actuator,
		Detail:    detail,
		Severity:  sev,
		At:        at,
	}
}
