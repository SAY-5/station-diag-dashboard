// Package ingest defines the canonical LogEvent type and the ingestion
// paths (TCP listener and file tail) that normalize raw test-station log
// lines into that type.
package ingest

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// LogEvent is the normalized internal representation of a single log line
// emitted by a test station. Both ingestion paths produce this struct.
type LogEvent struct {
	// Seq is a monotonic sequence number assigned by the hub on receipt.
	// It is zero until the event reaches the hub.
	Seq int64 `json:"seq"`

	// TS is the station-reported timestamp of the event.
	TS time.Time `json:"ts"`

	// StationID identifies the emitting test station.
	StationID string `json:"station_id"`

	// RunID groups events belonging to a single bench run.
	RunID string `json:"run_id"`

	// Level is the log severity: debug, info, warn, error.
	Level string `json:"level"`

	// Subsystem is the station subsystem, e.g. "actuator" or "power".
	Subsystem string `json:"subsystem"`

	// Message is the human-readable log message.
	Message string `json:"message"`

	// ActuatorID is set when the event concerns a specific actuator.
	ActuatorID string `json:"actuator_id,omitempty"`

	// Fields carries structured key/value data, e.g. {"current_a": 4.2}.
	Fields map[string]float64 `json:"fields,omitempty"`
}

// ErrEmptyLine is returned when a blank line is parsed.
var ErrEmptyLine = errors.New("ingest: empty log line")

// rawLine mirrors the wire JSON. Timestamps arrive as RFC3339 strings.
type rawLine struct {
	TS         string             `json:"ts"`
	StationID  string             `json:"station_id"`
	RunID      string             `json:"run_id"`
	Level      string             `json:"level"`
	Subsystem  string             `json:"subsystem"`
	Message    string             `json:"message"`
	ActuatorID string             `json:"actuator_id"`
	Fields     map[string]float64 `json:"fields"`
}

// ParseLine normalizes a single newline-delimited JSON log line into a
// LogEvent. Blank lines yield ErrEmptyLine; malformed JSON or a missing
// required field yields a descriptive error. Callers decide whether to
// skip or surface bad lines.
func ParseLine(line []byte) (LogEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return LogEvent{}, ErrEmptyLine
	}

	var raw rawLine
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return LogEvent{}, errors.New("ingest: malformed JSON: " + err.Error())
	}

	if raw.StationID == "" {
		return LogEvent{}, errors.New("ingest: missing station_id")
	}
	if raw.Level == "" {
		return LogEvent{}, errors.New("ingest: missing level")
	}

	ts := time.Now().UTC()
	if raw.TS != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw.TS)
		if err != nil {
			return LogEvent{}, errors.New("ingest: bad ts: " + err.Error())
		}
		ts = parsed.UTC()
	}

	level := strings.ToLower(raw.Level)
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return LogEvent{}, errors.New("ingest: unknown level: " + raw.Level)
	}

	return LogEvent{
		TS:         ts,
		StationID:  raw.StationID,
		RunID:      raw.RunID,
		Level:      level,
		Subsystem:  strings.ToLower(raw.Subsystem),
		Message:    raw.Message,
		ActuatorID: raw.ActuatorID,
		Fields:     raw.Fields,
	}, nil
}

// Field returns the value of a structured field and whether it was present.
func (e LogEvent) Field(name string) (float64, bool) {
	if e.Fields == nil {
		return 0, false
	}
	v, ok := e.Fields[name]
	return v, ok
}
