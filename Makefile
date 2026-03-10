.PHONY: build test lint generate clean

BIN := bin/jetscale
MODULE := github.com/Jetscale-ai/cli
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X '$(MODULE)/internal/cmd.version=$(VERSION)' \
	-X '$(MODULE)/internal/cmd.commit=$(COMMIT)' \
	-X '$(MODULE)/internal/cmd.date=$(DATE)'

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/jetscale

test:
	go test ./...

lint:
	golangci-lint run

generate:
	@echo "Regenerating OpenAPI client from openapi/spec.json ..."
	oapi-codegen -package generated \
		-generate types,client \
		-o internal/api/generated/client.gen.go \
		openapi/spec.json
	@echo "Done. Review changes in internal/api/generated/"

clean:
	rm -rf bin/

tidy:
	go mod tidy
