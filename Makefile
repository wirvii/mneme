# Makefile for mneme — pure-Go CLI (SQLite via modernc.org/sqlite, FTS5 built
# in by default). No CGO, no C compiler, no build tags required.

.PHONY: build install test test-race clean setup release-local

build:
	go build -o mneme ./cmd/mneme

install: build
	sudo cp mneme /usr/local/bin/

test:
	go test ./...

test-race:
	go test -race ./...

clean:
	rm -f mneme

setup: install
	mneme install claude-code

release-local:
	go build \
		-ldflags "-s -w -X github.com/wirvii/mneme/internal/cli.Version=local" \
		-o mneme ./cmd/mneme
