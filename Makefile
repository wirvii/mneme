# Makefile for mneme — Go CLI with CGO enabled (SQLite FTS5).
# Requires CGO_ENABLED=1. Supported platforms: Linux and macOS.

.PHONY: build install test test-race clean setup release-local

build:
	CGO_ENABLED=1 go build -tags fts5 -o mneme ./cmd/mneme

install: build
	sudo cp mneme /usr/local/bin/

test:
	CGO_ENABLED=1 go test -tags fts5 ./...

test-race:
	CGO_ENABLED=1 go test -tags fts5 -race ./...

clean:
	rm -f mneme

setup: install
	mneme install claude-code

release-local:
	CGO_ENABLED=1 go build -tags fts5 \
		-ldflags "-s -w -X github.com/juanftp/mneme/internal/cli.Version=local" \
		-o mneme ./cmd/mneme
