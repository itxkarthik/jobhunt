BIN      := bin/jobhunt
PKG      := ./cmd/jobhunt
GOBIN    := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

# Version details for `jobhunt version`: the nearest tag (empty before the
# first one, which shows as "dev"), the commit and today's date.
VPKG     := github.com/itxkarthik/jobhunt/internal/version
VERSION  ?= $(shell git describe --tags --dirty 2>/dev/null)
COMMIT   := $(shell git describe --always --dirty --abbrev=7 --exclude='*' 2>/dev/null)
DATE     := $(shell date -u +%Y-%m-%d)
LDFLAGS  := -X $(VPKG).Version=$(VERSION) -X $(VPKG).Commit=$(COMMIT) -X $(VPKG).Date=$(DATE)
LINT     := $(GOBIN)/golangci-lint
# Where `make install` puts jobhunt; ~/.local/bin survives Go upgrades.
PREFIX   ?= $(HOME)/.local/bin

.PHONY: all build test lint fmt vet run clean install

all: fmt vet test build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

install:
	mkdir -p $(PREFIX)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(PREFIX)/jobhunt $(PKG)
	@echo "installed $(PREFIX)/jobhunt"
	@command -v jobhunt >/dev/null || echo "note: add $(PREFIX) to your PATH"

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

$(LINT):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

lint: $(LINT)
	$(LINT) run ./...

run: build
	./$(BIN) search --country IN --type internship --area backend

clean:
	rm -rf bin dist
