BIN      := bin/jobhunt
PKG      := ./cmd/jobhunt
GOBIN    := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)
LINT     := $(GOBIN)/golangci-lint

.PHONY: all build test lint fmt vet run clean install

all: fmt vet test build

build:
	go build -trimpath -o $(BIN) $(PKG)

install:
	go install -trimpath $(PKG)

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
