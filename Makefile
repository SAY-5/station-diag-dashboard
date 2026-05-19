GO       ?= go
BIN      ?= bin
LDFLAGS  ?= -s -w
COVERMIN ?= 88
DRIFT    ?= 30

.PHONY: build dev test lint run emit up clean e2e race cover bench bench-regress

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/dashboard ./cmd/dashboard
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/station-emitter ./cmd/station-emitter

dev:
	$(GO) build ./...

test:
	$(GO) test ./...

e2e:
	RUN_E2E=1 $(GO) test -count=1 ./tests/e2e/...

race:
	$(GO) test -race -count=1 ./...

# cover runs the unit suite with cross-package coverage and fails the build
# if statement coverage of ./internal/... drops below COVERMIN percent.
cover:
	$(GO) test -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./tests/unit/...
	@total=$$($(GO) tool cover -func=coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	echo "internal coverage: $$total% (gate $(COVERMIN)%)"; \
	awk -v t=$$total -v m=$(COVERMIN) 'BEGIN { if (t+0 < m+0) { print "coverage below gate"; exit 1 } }'

# bench runs the ingest-to-fan-out throughput sweep and writes a timestamped
# JSON report under bench/results.
bench:
	$(GO) run ./cmd/bench

# bench-regress compares the two most recent bench reports and fails if any
# metric drifted past DRIFT percent. With one report it accepts the baseline.
bench-regress:
	DRIFT=$(DRIFT) scripts/bench-regress.sh

lint:
	golangci-lint run ./...

run:
	$(GO) run ./cmd/dashboard

emit:
	$(GO) run ./cmd/station-emitter -target localhost:7000

up:
	docker compose up --build

clean:
	rm -rf $(BIN)
	rm -f *.db *.db-journal *.db-wal *.db-shm
