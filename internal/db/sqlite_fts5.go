// sqlite_fts5.go imports go-sqlite3 with the fts5 build tag active.
// This file exists solely to document that the package requires FTS5 support
// compiled into the sqlite3 driver. Build this package (and the binary) with:
//
//	CGO_ENABLED=1 go build -tags fts5 ./...
//
// The fts5 tag is handled by mattn/go-sqlite3's own build-tag system; it
// recompiles the embedded C library with the SQLITE_ENABLE_FTS5 flag.
// Without this tag the memories_fts virtual table creation will fail at
// runtime with "no such module: fts5".
package db
