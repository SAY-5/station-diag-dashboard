package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/correlate"
	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
	"github.com/SAY-5/station-diag-dashboard/internal/store"
)

// windowSize bounds the sliding event window the rule engine evaluates.
// It is large enough to span the longest signature (3 consecutive events)
// across interleaved stations without growing without limit.
const windowSize = 256

// pipeline is the ingest.Sink that drives the diagnostic flow. Every event
// is persisted, fanned out to dashboards, then evaluated by the rule engine
// over a sliding window. Newly detected failures are persisted and pushed.
type pipeline struct {
	store  *store.Store
	hub    *hub.Hub
	engine *rules.Engine
	logger *slog.Logger

	mu         sync.Mutex
	window     []ingest.LogEvent
	seen       map[string]struct{}
	correlator *correlate.Correlator
}

func newPipeline(st *store.Store, h *hub.Hub, e *rules.Engine,
	corrWindow time.Duration, logger *slog.Logger) *pipeline {
	return &pipeline{
		store:      st,
		hub:        h,
		engine:     e,
		logger:     logger,
		seen:       map[string]struct{}{},
		correlator: correlate.New(corrWindow),
	}
}

// Publish satisfies ingest.Sink. It is safe for concurrent callers.
func (p *pipeline) Publish(ev ingest.LogEvent) {
	if err := p.store.RecordEvent(ev); err != nil {
		p.logger.Warn("persist event failed", "error", err)
	}
	p.hub.Publish(ev)

	p.mu.Lock()
	p.window = append(p.window, ev)
	if len(p.window) > windowSize {
		p.window = p.window[len(p.window)-windowSize:]
	}
	failures := p.engine.Evaluate(p.window)
	fresh := failures[:0:0]
	var incidents []correlate.Incident
	for _, f := range failures {
		k := f.RuleID + "|" + f.RunID + "|" + f.Actuator + "|" + f.Detail
		if _, ok := p.seen[k]; ok {
			continue
		}
		p.seen[k] = struct{}{}
		fresh = append(fresh, f)
		// The correlator is not concurrency-safe; it is only ever touched
		// here, under p.mu. Every fresh failure produces the incident it
		// belongs to, whether that incident is new or extended.
		inc, _ := p.correlator.Observe(f)
		incidents = append(incidents, inc)
	}
	p.mu.Unlock()

	for _, f := range fresh {
		if err := p.store.RecordFailure(f); err != nil {
			p.logger.Warn("persist failure failed", "error", err)
		}
		p.hub.PublishFailure(f)
		p.logger.Info("actuator failure flagged",
			"rule", f.RuleID, "run", f.RunID, "actuator", f.Actuator)
	}
	for _, inc := range incidents {
		if err := p.store.SaveIncident(inc); err != nil {
			p.logger.Warn("persist incident failed", "error", err)
		}
		p.hub.PublishIncident(inc)
		p.logger.Info("incident correlated",
			"incident", inc.ID, "run", inc.RunID,
			"root_cause", inc.RootCause, "members", len(inc.Members))
	}
}
