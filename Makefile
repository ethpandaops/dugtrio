# dugtrio
VERSION := $(shell git rev-parse --short HEAD)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell git rev-parse --short HEAD)

GOLDFLAGS += -X 'github.com/ethpandaops/dugtrio/utils.BuildVersion="$(VERSION)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dugtrio/utils.Buildtime="$(BUILDTIME)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dugtrio/utils.BuildRelease="$(RELEASE)"'

.PHONY: all test clean

all: build

test:
	go test ./...

build:
	@echo version: $(VERSION)
	go build -v -o bin/ -ldflags="-s -w $(GOLDFLAGS)" ./cmd/dugtrio-proxy

clean:
	rm -f bin/*
