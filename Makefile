GO      ?= go
BIN     ?= bin
LDFLAGS ?= -s -w

.PHONY: build dev test lint run emit up clean e2e

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/dashboard ./cmd/dashboard
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/station-emitter ./cmd/station-emitter

dev:
	$(GO) build ./...

test:
	$(GO) test ./...

e2e:
	RUN_E2E=1 $(GO) test -count=1 ./tests/e2e/...

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
