// Command dashboard runs the test-station diagnostics service: log
// ingestion, the actuator rule engine, the WebSocket hub, the REST API and
// the embedded web dashboard.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/api"
	"github.com/SAY-5/station-diag-dashboard/internal/config"
	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"
	"github.com/SAY-5/station-diag-dashboard/internal/store"
	"github.com/SAY-5/station-diag-dashboard/internal/web"
)

func main() {
	cfgPath := flag.String("config", "", "path to YAML config file (optional)")
	httpAddr := flag.String("http", "", "override HTTP listen address")
	tcpAddr := flag.String("tcp", "", "override TCP ingest address")
	dbPath := flag.String("db", "", "override SQLite database path")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	if *httpAddr != "" {
		cfg.HTTPAddr = *httpAddr
	}
	if *tcpAddr != "" {
		cfg.TCPAddr = *tcpAddr
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	if err := run(cfg, logger); err != nil {
		logger.Error("dashboard exited", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, logger *slog.Logger) error {
	ruleData, err := os.ReadFile(cfg.RulesFile)
	if err != nil {
		return errors.New("read rules file: " + err.Error())
	}
	engine, err := rules.Load(ruleData)
	if err != nil {
		return err
	}
	logger.Info("rule engine loaded", "rules", len(engine.Rules()))

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	h := hub.New(cfg.BacklogSize)

	// The diagnostic pipeline: every ingested event is persisted, fanned out
	// to dashboards, and fed through the rule engine over a sliding window.
	pipeline := newPipeline(st, h, engine, logger)

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	tcp := ingest.NewTCPListener(cfg.TCPAddr, pipeline, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := tcp.Run(ctx); err != nil {
			logger.Error("tcp ingest stopped", "error", err)
		}
	}()

	if cfg.TailFile != "" {
		tail := ingest.NewFileTail(cfg.TailFile, pipeline, logger)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tail.Run(ctx); err != nil {
				logger.Error("file ingest stopped", "error", err)
			}
		}()
	}

	apiServer := api.New(st, h, web.FS(), logger)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiServer.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("dashboard listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received, draining")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "error", err)
	}
	h.Shutdown(shutdownCtx)
	wg.Wait()
	logger.Info("shutdown complete")
	return nil
}
