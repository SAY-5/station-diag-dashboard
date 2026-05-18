// Command station-emitter simulates a test station emitting newline-
// delimited JSON log lines. It models a bench run as a sequence of actuator
// move commands, some of which deliberately fail so the dashboard rule
// engine has signatures to flag. Output goes to a TCP socket or stdout.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"
)

type logLine struct {
	TS         string             `json:"ts"`
	StationID  string             `json:"station_id"`
	RunID      string             `json:"run_id"`
	Level      string             `json:"level"`
	Subsystem  string             `json:"subsystem"`
	Message    string             `json:"message"`
	ActuatorID string             `json:"actuator_id,omitempty"`
	Fields     map[string]float64 `json:"fields,omitempty"`
}

func main() {
	station := flag.String("station", "station-1", "station identifier")
	target := flag.String("target", "", "dashboard TCP address, empty for stdout")
	runs := flag.Int("runs", 6, "number of bench runs to emit")
	interval := flag.Duration("interval", 350*time.Millisecond, "delay between log lines")
	seed := flag.Int64("seed", 0, "random seed, 0 uses the wall clock")
	flag.Parse()

	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(*seed))

	w, closer, err := openWriter(*target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "station-emitter:", err)
		os.Exit(1)
	}
	defer closer()

	emit := func(l logLine) {
		l.TS = time.Now().UTC().Format(time.RFC3339Nano)
		l.StationID = *station
		b, _ := json.Marshal(l)
		if _, werr := w.Write(append(b, '\n')); werr != nil {
			fmt.Fprintln(os.Stderr, "station-emitter: write:", werr)
			os.Exit(1)
		}
		time.Sleep(*interval)
	}

	for i := 0; i < *runs; i++ {
		runID := fmt.Sprintf("%s-run-%03d", *station, i+1)
		emitRun(emit, runID, rng)
	}
}

// emitRun emits one bench run. The run kind is chosen at random so a stream
// contains a mix of healthy runs and each actuator failure signature.
func emitRun(emit func(logLine), runID string, rng *rand.Rand) {
	actuator := fmt.Sprintf("act-%d", rng.Intn(4)+1)
	emit(logLine{RunID: runID, Level: "info", Subsystem: "controller",
		Message: "bench run started"})

	switch rng.Intn(4) {
	case 0:
		healthyRun(emit, runID, actuator)
	case 1:
		timeoutRun(emit, runID, actuator)
	case 2:
		overcurrentRun(emit, runID, actuator, rng)
	default:
		stuckRun(emit, runID, actuator)
	}

	emit(logLine{RunID: runID, Level: "info", Subsystem: "controller",
		Message: "bench run finished"})
}

func healthyRun(emit func(logLine), runID, actuator string) {
	for i := 0; i < 3; i++ {
		emit(logLine{RunID: runID, Level: "info", Subsystem: "actuator",
			ActuatorID: actuator, Message: "move_command issued"})
		emit(logLine{RunID: runID, Level: "info", Subsystem: "actuator",
			ActuatorID: actuator, Message: "position_reached confirmed"})
	}
}

func timeoutRun(emit func(logLine), runID, actuator string) {
	emit(logLine{RunID: runID, Level: "info", Subsystem: "actuator",
		ActuatorID: actuator, Message: "move_command issued"})
	emit(logLine{RunID: runID, Level: "warn", Subsystem: "actuator",
		ActuatorID: actuator, Message: "actuator still settling"})
	// No position_reached: the actuator_timeout signature fires.
	emit(logLine{RunID: runID, Level: "error", Subsystem: "actuator",
		ActuatorID: actuator, Message: "drive watchdog reset"})
}

func overcurrentRun(emit func(logLine), runID, actuator string, rng *rand.Rand) {
	emit(logLine{RunID: runID, Level: "info", Subsystem: "actuator",
		ActuatorID: actuator, Message: "move_command issued"})
	current := 3.8 + rng.Float64()
	emit(logLine{RunID: runID, Level: "warn", Subsystem: "actuator",
		ActuatorID: actuator, Message: "drive current sampled",
		Fields: map[string]float64{"current_a": current}})
	emit(logLine{RunID: runID, Level: "info", Subsystem: "actuator",
		ActuatorID: actuator, Message: "position_reached confirmed"})
}

func stuckRun(emit func(logLine), runID, actuator string) {
	for i := 0; i < 3; i++ {
		emit(logLine{RunID: runID, Level: "warn", Subsystem: "actuator",
			ActuatorID: actuator, Message: "move_command issued, retrying"})
	}
	// Three move_command with no position_reached: actuator_stuck fires.
	emit(logLine{RunID: runID, Level: "error", Subsystem: "actuator",
		ActuatorID: actuator, Message: "actuator carriage jammed"})
}

func openWriter(target string) (writer, func(), error) {
	if target == "" {
		return os.Stdout, func() {}, nil
	}
	var conn net.Conn
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		conn, err = net.Dial("tcp", target)
		if err == nil {
			return conn, func() { _ = conn.Close() }, nil
		}
		time.Sleep(time.Second)
	}
	return nil, nil, fmt.Errorf("dial %s: %w", target, err)
}

type writer interface {
	Write([]byte) (int, error)
}
