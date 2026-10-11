# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.

# Makefile contract (go-core-standards section 3). Heavy targets run under nice.
GO         ?= go
NICE       ?= nice -n 15
BIN        := $(CURDIR)/bin
FUZZTIME   ?= 30s
FUZZPAR    ?= 2
BENCHCOUNT ?= 6

GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.9.0
BENCHSTAT_VERSION     := v0.0.0-20261009192801-be2c69fb417e

.PHONY: tools generate lint test sim-test fuzz bench bench-compare mobile-check capi-check

tools: ## pinned tools into ./bin, never global
	GOBIN=$(BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	GOBIN=$(BIN) $(GO) install golang.org/x/perf/cmd/benchstat@$(BENCHSTAT_VERSION)

generate: ## STUB: no generated code yet (MixLink types arrive with link/ and contracts/mixlink)
	$(GO) generate ./...
	$(GO) mod tidy

lint:
	test -z "$$(gofmt -l .)"
	$(GO) vet ./...
	$(GO) vet -tags=sim ./...
	$(BIN)/golangci-lint run ./...
	$(BIN)/govulncheck ./...

test: ## unit: memnet + synctest, no real sockets
	$(NICE) $(GO) test -race -count=1 -short -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

sim-test: ## drivers and API against the simulators on real loopback sockets
	$(NICE) $(GO) test -race -count=1 -tags=sim -timeout=15m ./...

fuzz: ## every Fuzz target, one at a time (-fuzz accepts one package and one target)
	@for p in $$($(GO) list ./...); do for t in $$($(GO) test -list '^Fuzz' $$p | grep '^Fuzz'); do \
	  echo "== $$p $$t"; $(NICE) $(GO) test -run='^$$' -fuzz="^$$t$$" -fuzztime=$(FUZZTIME) -parallel=$(FUZZPAR) $$p || exit 1; \
	done; done

bench:
	$(NICE) $(GO) test -run='^$$' -bench=. -benchmem -count=$(BENCHCOUNT) ./... | tee bench.out

bench-compare: ## make bench-compare OLD=bench-main.out
	$(BIN)/benchstat $(OLD) bench.out

mobile-check: ## STUB: the gomobile package (mobile/) is the next spike; api/ already keeps a bindable surface
	@echo "mobile-check: no mobile/ package yet (stub, passes)"

capi-check: ## STUB: the c-shared package (capi/) is a later spike
	@echo "capi-check: no capi/ package yet (stub, passes)"
