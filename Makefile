# dugtrio
VERSION := $(shell git rev-parse --short HEAD)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

GOLDFLAGS += -X 'github.com/ethpandaops/dugtrio/utils.BuildVersion="$(VERSION)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dugtrio/utils.BuildTime="$(BUILDTIME)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dugtrio/utils.BuildRelease="$(RELEASE)"'

.PHONY: all test clean devnet devnet-run devnet-clean

all: build

test:
	go test ./...

build:
	@echo version: $(VERSION)
	go build -v -o bin/ -ldflags="-s -w $(GOLDFLAGS)" ./cmd/dugtrio-proxy

clean:
	rm -f bin/*

devnet:
	.hack/devnet/run.sh

devnet-run: devnet build
	go run cmd/dugtrio-proxy/main.go --config .hack/devnet/generated-dugtrio-config.yaml

devnet-clean:
	.hack/devnet/cleanup.sh
