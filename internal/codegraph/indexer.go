package codegraph

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ignoredDirs is the set of directory names the indexer always skips.
// External gitignore parsing is intentionally omitted to keep dependencies
// minimal; these cover the vast majority of generated/vendor/tool directories.
var ignoredDirs = map[string]struct{}{
	".git":        {},
	"vendor":      {},
	"node_modules": {},
	"dist":        {},
	"build":       {},
	".codegraph":  {},
	"testdata":    {},
}

// maxFileSize is the upper bound on file size the indexer will attempt to
// parse. Files larger than this are silently skipped to avoid loading large
// generated files (e.g. minified bundles, proto-generated code).
const maxFileSize = 1 << 20 // 1 MiB

// supportedExtensions maps file extensions to their detected language.
// The indexer uses this map to decide which files to process.
var supportedExtensions = map[string]string{
	".go":  "go",
	".ts":  "typescript",
	".tsx": "typescript",
	".js":  "javascript",
	".jsx": "javascript",
	".mjs": "javascript",
}

// IndexOptions configures a single index run over a directory tree.
type IndexOptions struct {
	// RootDir is the root of the source tree to walk (required).
	RootDir string

	// Language, when non-empty, overrides per-file language detection.
	// All eligible files are treated as this language.
	Language string

	// Force causes all eligible files to be re-indexed even when their
	// content hash matches the previously stored hash.
	Force bool

	// DryRun reports what would be indexed without writing to the store.
	DryRun bool
}

// Indexer orchestrates the extraction of code symbols from a directory tree
// and their persistence into the codegraph store. It operates incrementally:
// files whose content hash matches the stored record are skipped unless Force
// is true. Deleted files are pruned from the store on each run.
type Indexer struct {
	store *Store
}

// NewIndexer constructs an Indexer backed by the given Store.
func NewIndexer(store *Store) *Indexer {
	return &Indexer{store: store}
}

// Index walks opts.RootDir, extracts code symbols from each eligible source
// file, and writes the results to the store. It returns an IndexResult
// summarising what was scanned, indexed, skipped, and how many nodes and edges
// were created.
//
// Index never aborts on a single file error — it records the failure in
// FilesErrored and continues with the remaining files.
func (ix *Indexer) Index(opts IndexOptions) (*IndexResult, error) {
	start := time.Now()
	result := &IndexResult{}

	// Collect the relative paths of files found on disk so we can later
	// identify DB records for files that no longer exist.
	onDisk := make(map[string]struct{})

	walkErr := filepath.WalkDir(opts.RootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Cannot read this entry; skip it rather than aborting the walk.
			return nil
		}

		if d.IsDir() {
			if _, skip := ignoredDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}

		// Hidden files and Go-convention files that start with '_' are skipped.
		base := d.Name()
		if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
			return nil
		}

		ext := filepath.Ext(base)
		lang, supported := supportedExtensions[ext]
		if !supported {
			return nil
		}
		if opts.Language != "" {
			lang = opts.Language
		}

		// Compute path relative to RootDir. All store operations use relative paths
		// so that the index is portable across machines.
		relPath, err := filepath.Rel(opts.RootDir, path)
		if err != nil {
			return nil
		}

		result.FilesScanned++
		onDisk[relPath] = struct{}{}

		if err := ix.indexFile(path, relPath, lang, opts, result); err != nil {
			// Non-fatal: record the error and move on.
			result.FilesErrored++
		}

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("codegraph: indexer: walk %s: %w", opts.RootDir, walkErr)
	}

	// Cleanup phase: remove store records for files that no longer exist on disk.
	if !opts.DryRun {
		if err := ix.pruneDeleted(onDisk); err != nil {
			return nil, err
		}
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// indexFile handles the incremental check, extraction, and store write for a
// single source file. It updates result in-place.
func (ix *Indexer) indexFile(absPath, relPath, lang string, opts IndexOptions, result *IndexResult) error {
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", absPath, err)
	}

	// Skip files larger than 1 MiB.
	if info.Size() > maxFileSize {
		return nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", absPath, err)
	}

	// Compute the content hash to detect changes.
	sum := sha256.Sum256(content)
	hash := fmt.Sprintf("%x", sum[:])

	// Incremental check: skip if hash matches stored record and !Force.
	if !opts.Force {
		existing, err := ix.store.GetFile(relPath)
		if err != nil {
			return fmt.Errorf("get file record %s: %w", relPath, err)
		}
		if existing != nil && existing.ContentHash == hash {
			result.FilesSkipped++
			return nil
		}
	}

	// Get the extractor for this language.
	extractor := GetExtractor(lang)
	if extractor == nil {
		// No extractor registered — skip silently (not an error).
		return nil
	}

	// Extract code symbols from file content.
	extraction, err := extractor.Extract(relPath, content)
	if err != nil {
		// Extraction error is non-fatal; partial results may still be valid.
		result.FilesErrored++
		// Do not return here; fall through to persist partial results if any.
	}
	if extraction == nil {
		return nil
	}

	if opts.DryRun {
		// In dry-run mode we count but do not persist anything.
		result.FilesIndexed++
		result.NodesCreated += len(extraction.Nodes)
		result.EdgesCreated += len(extraction.Edges)
		return nil
	}

	// Persist: delete stale nodes for this file, then batch-insert the new set.
	if _, delErr := ix.store.DeleteNodesByFile(relPath); delErr != nil {
		return fmt.Errorf("delete nodes for %s: %w", relPath, delErr)
	}
	if uErr := ix.store.BatchUpsertNodes(extraction.Nodes); uErr != nil {
		return fmt.Errorf("batch upsert nodes for %s: %w", relPath, uErr)
	}
	if uErr := ix.store.BatchUpsertEdges(extraction.Edges); uErr != nil {
		return fmt.Errorf("batch upsert edges for %s: %w", relPath, uErr)
	}

	errMsg := ""
	if len(extraction.Errors) > 0 {
		msgs := make([]string, len(extraction.Errors))
		for i, e := range extraction.Errors {
			msgs[i] = e.Message
		}
		errMsg = strings.Join(msgs, "; ")
	}

	fr := FileRecord{
		Path:        relPath,
		ContentHash: hash,
		Language:    lang,
		Size:        info.Size(),
		ModifiedAt:  info.ModTime().Unix(),
		IndexedAt:   time.Now().Unix(),
		NodeCount:   len(extraction.Nodes),
		Errors:      errMsg,
	}
	if uErr := ix.store.UpsertFile(fr); uErr != nil {
		return fmt.Errorf("upsert file record %s: %w", relPath, uErr)
	}

	result.FilesIndexed++
	result.NodesCreated += len(extraction.Nodes)
	result.EdgesCreated += len(extraction.Edges)
	return nil
}

// pruneDeleted removes store records (file + nodes) for paths that exist in the
// database but no longer exist on disk. This keeps the graph consistent after
// files are renamed or deleted.
func (ix *Indexer) pruneDeleted(onDisk map[string]struct{}) error {
	stored, err := ix.store.ListFiles()
	if err != nil {
		return fmt.Errorf("codegraph: indexer: list files: %w", err)
	}

	for _, fr := range stored {
		if _, exists := onDisk[fr.Path]; !exists {
			if _, err := ix.store.DeleteNodesByFile(fr.Path); err != nil {
				return fmt.Errorf("codegraph: indexer: delete nodes for deleted file %s: %w", fr.Path, err)
			}
			if err := ix.store.DeleteFile(fr.Path); err != nil {
				return fmt.Errorf("codegraph: indexer: delete file record %s: %w", fr.Path, err)
			}
		}
	}
	return nil
}
