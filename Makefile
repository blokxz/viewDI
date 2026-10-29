# viewDI developer targets. See CLAUDE.md for the full list.

GO        ?= go
BIN       := bin/viewdi
PKG       := ./cmd/viewdi
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
FUZZTIME  ?= 30s
GOBIN     := $(shell $(GO) env GOPATH)/bin

.PHONY: build run mock chaos test test-integration golden-update fuzz bench lint fmt cover \
        fixtures docs-venv docs-diagrams clean

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

run: build
	./$(BIN)

mock:
	$(GO) run $(PKG) --mock

chaos:
	$(GO) run $(PKG) --mock=chaos

test:
	$(GO) test -race -shuffle=on ./...

test-integration:
	$(GO) test -race -tags=integration ./...

# Only packages with golden tests understand -update.
GOLDEN_PKGS := ./internal/app/
golden-update:
	$(GO) test $(GOLDEN_PKGS) -run TestGolden -update

# Runs every fuzz target for FUZZTIME (go test allows one target per run).
fuzz:
	@set -e; for pkg in $$($(GO) list ./...); do \
	  for t in $$($(GO) test -list '^Fuzz' $$pkg | grep '^Fuzz' || true); do \
	    echo "== $$pkg $$t"; $(GO) test -run '^$$' -fuzz "^$$t$$" -fuzztime $(FUZZTIME) $$pkg; \
	  done; \
	done

bench:
	$(GO) test -run '^$$' -bench . -benchmem -count 6 ./... | tee bench.txt

# Records sanitised API fixtures from a real daemon: make fixtures ENGINE=incus
ENGINE ?= docker
fixtures:
	$(GO) run -tags fixturerec ./cmd/fixturerec -engine $(ENGINE)

lint:
	$(GOBIN)/golangci-lint run ./...

fmt:
	$(GOBIN)/gofumpt -w .
	$(GOBIN)/goimports -local github.com/blokxz/viewDI -w .

cover:
	$(GO) test -coverprofile=cover.out ./...
	$(GO) tool cover -func=cover.out

docs-venv:
	python3 -m venv docs/diagrams/.venv
	docs/diagrams/.venv/bin/pip install -r docs/diagrams/requirements.txt

docs-diagrams:
	docs/diagrams/.venv/bin/python docs/diagrams/generate.py

clean:
	rm -rf bin cover.out bench.txt
