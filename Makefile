BIN      := bin/jobhunt
PKG      := ./cmd/jobhunt
GOBIN    := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)
LINT     := $(GOBIN)/golangci-lint
# Where `make install` puts jobhunt; ~/.local/bin survives Go upgrades.
PREFIX   ?= $(HOME)/.local/bin

.PHONY: all build test lint fmt vet run clean install

all: fmt vet test build

build:
	go build -trimpath -o $(BIN) $(PKG)

install:
	mkdir -p $(PREFIX)
	go build -trimpath -o $(PREFIX)/jobhunt $(PKG)
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
