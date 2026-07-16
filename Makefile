# Makefile for mneme — pure-Go CLI (SQLite via modernc.org/sqlite, FTS5 built
# in by default). No CGO, no C compiler, no build tags required.

.PHONY: build install test test-race clean setup release-local

# TEST_HOME sandboxes HOME/USERPROFILE for the test suite (SPEC-085 G2): tests
# must never resolve the real ~/.mneme (or the real shared team-memory vault
# derived from it) — only db.OpenMemory()/t.TempDir() or an explicit
# t.Setenv("HOME", …) override are allowed to touch a "real" DB path.
#
# REAL_GOCACHE/REAL_GOMODCACHE are resolved via $(shell …) at Makefile
# parse time — i.e. using make's own ambient (real) environment — and
# then passed through explicitly in TEST_ENV. Both derive from HOME by
# default (SPEC-085 R2): a naive `HOME=$(TEST_HOME) GOCACHE=$$(go env
# GOCACHE) go test` on a single shell line does NOT work, because bash
# evaluates same-line VAR=value assignments left-to-right, so `go env
# GOCACHE` would already observe the just-set sandboxed HOME and resolve
# a cache path INSIDE $(TEST_HOME) — forcing a full rebuild (and, worse, a
# full module re-download into $(TEST_HOME)/go/pkg/mod) on every run.
REAL_GOCACHE := $(shell go env GOCACHE)
REAL_GOMODCACHE := $(shell go env GOMODCACHE)
TEST_HOME := $(CURDIR)/tmp/testhome
TEST_ENV := TEST_HOME="$(TEST_HOME)" HOME="$(TEST_HOME)" USERPROFILE="$(TEST_HOME)" \
	GOCACHE="$(REAL_GOCACHE)" GOMODCACHE="$(REAL_GOMODCACHE)"

build:
	go build -o mneme ./cmd/mneme

install: build
	sudo cp mneme /usr/local/bin/

test:
	@mkdir -p "$(TEST_HOME)"
	$(TEST_ENV) go test ./...
	@TEST_HOME="$(TEST_HOME)" ./scripts/testguard.sh

test-race:
	@mkdir -p "$(TEST_HOME)"
	$(TEST_ENV) go test -race ./...
	@TEST_HOME="$(TEST_HOME)" ./scripts/testguard.sh

clean:
	rm -f mneme

setup: install
	mneme install claude-code

release-local:
	go build \
		-ldflags "-s -w -X github.com/wirvii/mneme/internal/cli.Version=local" \
		-o mneme ./cmd/mneme
