# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.

# Makefile contract (go-core-standards section 3). Heavy targets run under nice.
GO         ?= go
NICE       ?= nice -n 15
# Pinned tools: make tools installs them into ./bin. In a git worktree
# without its own ./bin the main checkout's bin is used (the same pins);
# BIN=<dir> on the command line overrides both.
TOOLS_BIN  := $(CURDIR)/bin
GIT_COMMON := $(shell git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
MAIN_BIN   := $(if $(GIT_COMMON),$(abspath $(GIT_COMMON)/../bin))
BIN        ?= $(if $(wildcard $(TOOLS_BIN)/golangci-lint),$(TOOLS_BIN),$(if $(wildcard $(MAIN_BIN)/golangci-lint),$(MAIN_BIN),$(TOOLS_BIN)))
FUZZTIME   ?= 30s
FUZZPAR    ?= 2
BENCHCOUNT ?= 6

GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.9.0
BENCHSTAT_VERSION     := v0.0.0-20261009192801-be2c69fb417e

# Bindings (go-core-standards section 10, apple-standards section 3).
BUILD        := $(CURDIR)/build
XCFRAMEWORK  := $(BUILD)/Mixcore.xcframework
IOS_MIN      := 17.0
MACOS_MIN    := 14.0
CAPI_DIR     := $(BUILD)/capi
CAPITEST_DIR := $(BUILD)/capitest
HOST_OS      := $(shell $(GO) env GOOS)
ifeq ($(HOST_OS),darwin)
CAPI_LIB     := libmixcore.dylib
CAPI_LDFLAGS := -s -w -extldflags=-Wl,-install_name,@rpath/libmixcore.dylib
else
CAPI_LIB     := libmixcore.so
CAPI_LDFLAGS := -s -w -extldflags=-Wl,-soname,libmixcore.so
endif

.PHONY: tools mobile-tools generate lint test sim-test fuzz bench bench-compare mobile-check capi-check xcframework

tools: mobile-tools ## pinned tools into ./bin, never global
	GOBIN=$(TOOLS_BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(TOOLS_BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	GOBIN=$(TOOLS_BIN) $(GO) install golang.org/x/perf/cmd/benchstat@$(BENCHSTAT_VERSION)

mobile-tools: ## gomobile and gobind into ./bin at the versions pinned by the go.mod tool lines
	GOBIN=$(TOOLS_BIN) $(GO) install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind

generate: ## STUB: no generated code yet (MixLink types arrive with link/ and contracts/mixlink)
	$(GO) generate ./...
	$(GO) mod tidy

lint:
	@test -x $(BIN)/golangci-lint -a -x $(BIN)/govulncheck || { echo "lint: no pinned tools in $(BIN); run make tools (or BIN=<checkout>/bin)"; exit 1; }
	test -z "$$(gofmt -l .)"
	$(GO) vet ./...
	$(GO) vet -tags=sim,slow ./...
	$(BIN)/golangci-lint run ./...
	$(BIN)/govulncheck ./...

test: ## unit: memnet + synctest, no real sockets
	$(NICE) $(GO) test -race -count=1 -short -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

sim-test: ## drivers and API against the simulators on real loopback sockets
	$(NICE) $(GO) test -race -count=1 -tags=sim,slow -timeout=15m ./...

fuzz: ## every Fuzz target, one at a time (-fuzz accepts one package and one target)
	@for p in $$($(GO) list ./...); do for t in $$($(GO) test -list '^Fuzz' $$p | grep '^Fuzz'); do \
	  echo "== $$p $$t"; $(NICE) $(GO) test -run='^$$' -fuzz="^$$t$$" -fuzztime=$(FUZZTIME) -parallel=$(FUZZPAR) $$p || exit 1; \
	done; done

bench:
	$(NICE) $(GO) test -run='^$$' -bench=. -benchmem -count=$(BENCHCOUNT) ./... | tee bench.out

bench-compare: ## make bench-compare OLD=bench-main.out
	$(BIN)/benchstat $(OLD) bench.out

mobile-check: ## gobind type check of mobile/ (no Xcode, no NDK); fails when gobind skips any symbol
	@out=$$(mktemp -d) && PATH=$(BIN):$$PATH $(BIN)/gobind -lang=go,objc,java -outdir $$out ./mobile && \
	if grep -rn "skipped" $$out; then echo "mobile-check: gobind skipped the symbols above"; rm -rf $$out; exit 1; fi; \
	rm -rf $$out; echo "mobile-check: ok, every exported symbol of mobile/ is bindable"

capi-check: ## host c-shared library + header into build/capi, capitest build, Go tests, C smoke test against mixsim
	CGO_ENABLED=1 $(NICE) $(GO) build -buildmode=c-shared -trimpath -ldflags='$(CAPI_LDFLAGS)' -o $(CAPI_DIR)/$(CAPI_LIB) ./capi
	CGO_ENABLED=1 $(NICE) $(GO) build -buildmode=c-shared -trimpath -tags capitest -ldflags='$(CAPI_LDFLAGS)' -o $(CAPITEST_DIR)/$(CAPI_LIB) ./capi
	$(NICE) $(GO) build -o $(CAPITEST_DIR)/mixsim ./cmd/mixsim
	CGO_ENABLED=1 $(NICE) $(GO) test -race -count=1 -tags capitest ./capi
	$(NICE) sh capi/testdata/smoke.sh $(CAPITEST_DIR) $(CAPITEST_DIR)/mixsim

xcframework: ## Apple xcframework of mobile/ (iOS, iOS simulator, macOS) into build/; needs full Xcode (DEVELOPER_DIR)
	@xcrun --sdk iphoneos --show-sdk-path >/dev/null 2>&1 || { \
	  echo "xcframework: no iOS SDK. Point DEVELOPER_DIR at a full Xcode, e.g."; \
	  echo "  make xcframework DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer"; exit 1; }
	rm -rf $(XCFRAMEWORK)
	PATH=$(BIN):$$PATH $(NICE) $(BIN)/gomobile bind -target=ios,iossimulator,macos \
	  -iosversion $(IOS_MIN) -macosversion $(MACOS_MIN) -trimpath -ldflags='-s -w' -o $(XCFRAMEWORK) ./mobile
