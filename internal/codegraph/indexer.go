package codegraph

import (
	"crypto/sha256"
	"errors"
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
//
// Hidden directories (names starting with ".") are skipped unconditionally in
// the WalkDir branch — see the inline comment inside Index. The entries below
// that start with "." are kept for documentation of intent and defense-in-depth;
// they would be skipped even without the explicit map lookup. The only entry
// here that is NOT covered by the hidden-dir skip is "coverage".
var ignoredDirs = map[string]struct{}{
	".git":         {},
	"vendor":       {},
	"node_modules": {},
	"dist":         {},
	"build":        {},
	".codegraph":   {},
	"testdata":     {},
	// Framework/toolchain build and cache directories — inequivocally generated.
	".next":       {}, // Next.js build output
	".turbo":      {}, // Turborepo cache
	".svelte-kit": {}, // SvelteKit build output
	".nuxt":       {}, // Nuxt build output
	".cache":      {}, // generic tool cache (Babel, ESLint, Parcel, etc.)
	"coverage":    {}, // test coverage output (not hidden; needs explicit entry)
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

	// Changes, when non-nil, activates scoped mode (SPEC-101): the indexer does
	// NOT walk RootDir and instead processes exactly this list of changed files
	// (added/modified → extract, deleted → purge, renamed → purge old + extract
	// new). A nil slice (the default) preserves the full walk-the-tree behaviour
	// byte-for-byte. Force takes precedence: when Force is true the indexer does a
	// full scan even if Changes is set.
	Changes []ChangedFile

	// Include, when non-nil, replaces the filesystem tree walk with an explicit
	// list of candidate paths (relative to RootDir) for a FULL scan (SPEC-102).
	// The CLI populates it from `git ls-files` so the index honours .gitignore
	// without this package knowing anything about git. Each path is still run
	// through IsEligibleSource, and pruneDeleted STILL runs (unlike scoped mode
	// via Changes), so nodes for paths no longer in the list are purged — this
	// is what auto-heals a graph previously polluted by a gitignored directory.
	// A nil slice (the default) preserves the legacy walk (the non-git
	// fallback). Include is ignored when Changes activates scoped mode.
	Include []string
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
// Index does not abort on an ordinary per-file error — it records the
// failure in FilesErrored and continues with the remaining files.
// ErrExtractorIncompatible (a language's extractor toolchain being present but
// unusable) is likewise never fatal to the run (SPEC-142 D3, superseding
// SPEC-088 D4's abort): the first file of a language that fails this way
// marks that language DEGRADED for the rest of THIS run — every further file
// of that language is skipped (D4: skipped BEFORE a new extractor instance is
// ever requested, never after one fails again) and counted in
// result.FilesDegraded, never FilesErrored, since the two are different
// natures (systemic toolchain absence vs. an ordinary per-file failure). Index
// still returns (result, nil) in this case; the degraded languages are both
// reported on the result (result.DegradedLanguages) and persisted to the
// store so every later query can declare the graph incomplete (see Notice).
func (ix *Indexer) Index(opts IndexOptions) (*IndexResult, error) {
	// Scoped mode (SPEC-101): a non-nil Changes list means the caller already
	// computed the delta (e.g. from a git diff), so skip the tree walk entirely.
	// Force overrides scoped mode and forces a full scan.
	if opts.Changes != nil && !opts.Force {
		return ix.indexScoped(opts)
	}

	// Full-scan-by-list (SPEC-102): a non-nil Include means the caller already
	// computed the git-eligible file list (tracked + untracked-but-not-ignored),
	// so replace the tree walk with an iteration over that list. Unlike scoped
	// mode, pruneDeleted still runs — this is a full scan, just driven by a
	// precomputed candidate set instead of filepath.WalkDir.
	if opts.Include != nil {
		return ix.indexList(opts)
	}

	start := time.Now()
	result := &IndexResult{}

	// Collect the relative paths of files found on disk so we can later
	// identify DB records for files that no longer exist.
	onDisk := make(map[string]struct{})

	// degraded tracks, for THIS run only, which languages have already hit
	// ErrExtractorIncompatible (SPEC-142 D3/D4). Keyed by language.
	degraded := make(map[string]*DegradedLanguage)

	walkErr := filepath.WalkDir(opts.RootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Cannot read this entry; skip it rather than aborting the walk.
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			// Skip hidden directories (tooling/VCS/build caches: .next, .turbo,
			// .svelte-kit, .git, .codegraph, etc.). The guard "name != '.'"
			// is defensive: filepath.WalkDir does not deliver "." as Name() for
			// the walk root itself when the root is the supplied path, but we
			// keep it to be safe in case the root directory itself starts with ".".
			if name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, skip := ignoredDirs[name]; skip {
				return filepath.SkipDir
			}
			return nil
		}

		// Compute path relative to RootDir. All store operations use relative paths
		// so that the index is portable across machines.
		relPath, err := filepath.Rel(opts.RootDir, path)
		if err != nil {
			return nil
		}

		// Eligibility is decided by the shared predicate so the full walk and the
		// scoped path agree exactly on what counts as an indexable source file.
		lang, ok := IsEligibleSource(relPath)
		if !ok {
			return nil
		}
		if opts.Language != "" {
			lang = opts.Language
		}

		result.FilesScanned++
		// A degraded file still counts as present on disk (SPEC-142 D5): its
		// PREVIOUSLY indexed nodes, if any, must survive pruneDeleted below —
		// this spec never destroys knowledge it could not re-verify.
		onDisk[relPath] = struct{}{}

		ix.indexEligibleFile(path, relPath, lang, opts, result, degraded)

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

	if err := ix.persistDegraded(degraded, opts, result); err != nil {
		return nil, err
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// indexList performs a full scan driven by a precomputed candidate list
// (opts.Include) instead of filepath.WalkDir (SPEC-102). It exists to let the
// CLI honour .gitignore (via `git ls-files`) without teaching this
// git-agnostic package anything about git: the caller resolves the list of
// paths git considers part of the working tree, and indexList applies exactly
// the same eligibility, extraction, and pruning logic the walk does.
//
// Each entry is still filtered through IsEligibleSource — belt-and-suspenders
// against a caller-supplied path that falls under a hidden or ignoredDirs
// directory (e.g. a tracked file inside vendor/) — so the walk and the list
// path agree exactly on what is indexable, mirroring the walk/scoped
// symmetry documented on IsEligibleSource.
//
// Unlike indexScoped (Changes), indexList still runs pruneDeleted: it is a
// full scan, so any store record whose path is absent from the (now
// git-filtered) list is stale and must be purged. This is what auto-heals a
// graph previously polluted by a gitignored directory that the legacy walk
// used to descend into — the next full-scan run with Include populated no
// longer sees those paths in onDisk and prunes them.
func (ix *Indexer) indexList(opts IndexOptions) (*IndexResult, error) {
	start := time.Now()
	result := &IndexResult{}
	onDisk := make(map[string]struct{})

	// degraded tracks, for THIS run only, which languages have already hit
	// ErrExtractorIncompatible (SPEC-142 D3/D4). Keyed by language.
	degraded := make(map[string]*DegradedLanguage)

	for _, rel := range opts.Include {
		rel = filepath.Clean(rel)

		lang, ok := IsEligibleSource(rel)
		if !ok {
			continue
		}
		if opts.Language != "" {
			lang = opts.Language
		}

		result.FilesScanned++
		// A degraded file still counts as present on disk (SPEC-142 D5).
		onDisk[rel] = struct{}{}

		ix.indexEligibleFile(filepath.Join(opts.RootDir, rel), rel, lang, opts, result, degraded)
	}

	if !opts.DryRun {
		if err := ix.pruneDeleted(onDisk); err != nil {
			return nil, err
		}
	}

	if err := ix.persistDegraded(degraded, opts, result); err != nil {
		return nil, err
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// IsEligibleSource reports whether the file at relPath (a path relative to the
// index root) is one the indexer should process, and if so its detected
// language. It is the single source of truth shared by the full walk and the
// scoped path (SPEC-101), so both agree exactly on what is indexable:
//
//   - no path component may be a hidden directory (leading ".") or one of the
//     ignoredDirs (vendor, node_modules, build caches, …);
//   - the base name must not start with "." (hidden) or "_" (Go-convention
//     ignored file);
//   - the extension must map to a supported language.
//
// The directory-component check mirrors the SkipDir logic in Index's WalkDir
// callback; in the walk it is redundant (ignored dirs are pruned before their
// files are visited) but harmless, and it is essential in scoped mode where
// there is no walk to prune anything.
//
// Exported since SPEC-118 (D6 point 4): the budget mechanism
// (internal/quality/internal/service) needs the SAME elegibilidad this
// indexer already enforces — a `.md`, a `.sql`, or a file under `vendor/`
// contributes no budgetable symbols and must produce no finding for not
// contributing them. Reimplementing this predicate in another package would
// create the second source of truth this function's own name exists to
// prevent; its LOGIC is unchanged by this export, only its visibility.
func IsEligibleSource(relPath string) (lang string, ok bool) {
	slashed := filepath.ToSlash(relPath)
	parts := strings.Split(slashed, "/")
	for _, dir := range parts[:len(parts)-1] {
		if dir == "" || dir == "." {
			continue
		}
		if strings.HasPrefix(dir, ".") {
			return "", false
		}
		if _, skip := ignoredDirs[dir]; skip {
			return "", false
		}
	}

	base := parts[len(parts)-1]
	if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
		return "", false
	}

	lang, ok = supportedExtensions[filepath.Ext(base)]
	if !ok {
		return "", false
	}
	return lang, true
}

// indexScoped processes a pre-computed list of changed files (opts.Changes)
// without walking the tree (SPEC-101). Added/modified files are extracted via
// the same indexFile path used by the full scan (idempotent: it deletes stale
// nodes then re-inserts); deleted files have their symbols purged; renamed files
// have the old path purged and the new path extracted.
//
// It deliberately does NOT call pruneDeleted — that is full-scan-only detection
// (it would wipe every file absent from the small change set). In scoped mode
// deletions are explicit, carried by ChangeDeleted / ChangeRenamed entries.
func (ix *Indexer) indexScoped(opts IndexOptions) (*IndexResult, error) {
	start := time.Now()
	result := &IndexResult{}

	// degraded tracks, for THIS run only, which languages have already hit
	// ErrExtractorIncompatible (SPEC-142 D3/D4). Keyed by language.
	degraded := make(map[string]*DegradedLanguage)

	for _, ch := range opts.Changes {
		switch ch.Status {
		case ChangeDeleted:
			// A delete purges unconditionally: DeleteNodesByFile/DeleteFile are
			// no-ops when the path was never indexed or was ineligible.
			if err := ix.purgeFile(ch.Path, opts, result); err != nil {
				return nil, err
			}

		case ChangeRenamed:
			// Purge the old path, then re-extract the new path if it is eligible.
			if err := ix.purgeFile(ch.OldPath, opts, result); err != nil {
				return nil, err
			}
			ix.indexScopedFile(ch.Path, opts, result, degraded)

		default:
			// ChangeAdded / ChangeModified.
			ix.indexScopedFile(ch.Path, opts, result, degraded)
		}
	}

	// SPEC-142 D6: a scoped run only ever ADDS or UPDATES the degraded-language
	// mark — it never clears it, because it only ever saw its own delta, not
	// every eligible file of the languages already marked.
	if err := ix.persistDegraded(degraded, opts, result); err != nil {
		return nil, err
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// indexScopedFile extracts a single changed file if it is eligible, reusing the
// full-scan indexFile so the two paths stay behaviourally identical (incremental
// content-hash skip included). Ineligible paths (unsupported extension, ignored
// directory) are silently ignored — they are simply not part of the graph.
// ErrExtractorIncompatible is handled by the shared indexEligibleFile exactly
// as it is in full-scan mode (SPEC-142 D3): it marks the language degraded for
// the rest of this run instead of aborting scoped mode — aborting here is
// MORE damaging than in a full scan, since scoped mode is precisely what a
// git hook runs after every commit (SPEC-142 O2).
func (ix *Indexer) indexScopedFile(relPath string, opts IndexOptions, result *IndexResult, degraded map[string]*DegradedLanguage) {
	lang, ok := IsEligibleSource(relPath)
	if !ok {
		return
	}
	if opts.Language != "" {
		lang = opts.Language
	}

	result.FilesScanned++
	ix.indexEligibleFile(filepath.Join(opts.RootDir, relPath), relPath, lang, opts, result, degraded)
}

// purgeFile removes a file's symbols from the graph (deleted or renamed-away
// path). DeleteNodesByFile cascades to edges and unresolved_refs; DeleteFile
// drops the file record. Both are idempotent no-ops when the path is absent, so
// purging an ineligible or never-indexed path is safe. In DryRun mode nothing is
// written; the counter is still bumped so callers can see what would happen.
func (ix *Indexer) purgeFile(relPath string, opts IndexOptions, result *IndexResult) error {
	if opts.DryRun {
		result.FilesDeleted++
		return nil
	}
	if _, err := ix.store.DeleteNodesByFile(relPath); err != nil {
		return fmt.Errorf("codegraph: indexer: scoped delete nodes for %s: %w", relPath, err)
	}
	if err := ix.store.DeleteFile(relPath); err != nil {
		return fmt.Errorf("codegraph: indexer: scoped delete file record %s: %w", relPath, err)
	}
	result.FilesDeleted++
	return nil
}

// indexEligibleFile is the SINGLE place all three entry points (Index,
// indexList, indexScoped via indexScopedFile) call to process one already-
// eligible file, so the SPEC-142 D3/D4 degradation logic exists exactly once.
//
// D4 — the batch trap: if lang is ALREADY tracked in degraded (some earlier
// file in this same run already hit ErrExtractorIncompatible for it), this
// file is skipped WITHOUT ever calling indexFile — and therefore without ever
// requesting a new extractor instance via GetExtractor. This is not an
// optimisation: GetExtractor's registry returns a brand-new Extractor
// instance on every call (see extractor.go's factory pattern), so a fatal
// condition inside one instance buys nothing for the next file's instance.
// Skipping BEFORE the call is the only thing that keeps a repository with
// thousands of files of a degraded language from spawning thousands of
// subprocesses that each fail on their own.
//
// Only when lang is not yet tracked does this call indexFile at all. If that
// call returns ErrExtractorIncompatible, the language is recorded into
// degraded (first-seen now) and the file is counted in result.FilesDegraded —
// never result.FilesErrored, a different nature entirely (SPEC-142 D3). Any
// other error is an ordinary per-file failure, unchanged from before this
// spec: counted in result.FilesErrored, and processing continues regardless.
func (ix *Indexer) indexEligibleFile(absPath, relPath, lang string, opts IndexOptions, result *IndexResult, degraded map[string]*DegradedLanguage) {
	if dl, tracked := degraded[lang]; tracked {
		dl.FilesSkippedLastRun++
		result.FilesDegraded++
		return
	}

	err := ix.indexFile(absPath, relPath, lang, opts, result)
	if err == nil {
		return
	}
	if !errors.Is(err, ErrExtractorIncompatible) {
		result.FilesErrored++
		return
	}

	now := time.Now().Unix()
	degraded[lang] = &DegradedLanguage{
		Language:            lang,
		Cause:               CauseToolchainIncompatible,
		Reason:              clampReason(err.Error()),
		FilesSkippedLastRun: 1,
		FirstSeenUnix:       now,
		LastSeenUnix:        now,
	}
	result.FilesDegraded++
}

// persistDegraded reconciles this run's locally observed degraded languages
// (degraded, built up by indexEligibleFile — possibly empty) with whatever is
// already persisted in the store, and always populates
// result.DegradedLanguages so a caller sees what this run found even when
// nothing is written (SPEC-142 D19).
//
// opts.DryRun short-circuits before any store access: dry-run never persists
// and never clears (D19).
//
// Otherwise, SPEC-142 D6 governs whether this run may CLEAR a language no
// longer found degraded: only a genuine full scan — opts.Changes == nil (not
// scoped mode) AND opts.Language == "" (nothing forced every file to one
// language) — REPLACES the stored record with exactly this run's findings,
// preserving FirstSeenUnix for any language that remains degraded. A scoped
// run (or one with opts.Language forced) only ever ADDS or UPDATES entries
// for languages it actually observed this run; every other previously-
// recorded language survives untouched, because a scoped run only ever saw
// its own delta and cannot know whether an untouched language actually
// healed.
func (ix *Indexer) persistDegraded(degraded map[string]*DegradedLanguage, opts IndexOptions, result *IndexResult) error {
	for _, dl := range degraded {
		result.DegradedLanguages = append(result.DegradedLanguages, *dl)
	}
	sortDegradedLanguages(result.DegradedLanguages)

	if opts.DryRun {
		return nil
	}

	isFullScan := opts.Changes == nil && opts.Language == ""

	if !isFullScan && len(degraded) == 0 {
		// A scoped run that observed nothing degraded has nothing to add or
		// update, and D6 forbids it from clearing anything — skip the store
		// entirely rather than touching it for a pure no-op. This is also
		// what keeps the common case (a healthy repository's git-hook-driven
		// scoped re-index) from paying a read on every single commit.
		return nil
	}

	existing, err := ix.store.GetDegradedLanguages()
	if err != nil {
		return fmt.Errorf("codegraph: indexer: read degraded languages: %w", err)
	}
	existingByLang := make(map[string]DegradedLanguage, len(existing))
	for _, e := range existing {
		existingByLang[e.Language] = e
	}

	var final []DegradedLanguage
	if isFullScan {
		// A full scan re-examined every eligible file, so it can assert the
		// complete truth: replace the stored record with exactly what this
		// run found, carrying forward FirstSeenUnix for languages still
		// degraded (their history is not reset just because the record was
		// rewritten).
		for _, dl := range degraded {
			rec := *dl
			if prev, ok := existingByLang[dl.Language]; ok {
				rec.FirstSeenUnix = prev.FirstSeenUnix
			}
			final = append(final, rec)
		}
	} else {
		// Scoped (or language-forced): start from everything already
		// recorded, then merge in only what this run actually observed.
		// Nothing is ever removed here.
		final = existing
		for i := range final {
			if dl, ok := degraded[final[i].Language]; ok {
				final[i].Cause = dl.Cause
				final[i].Reason = dl.Reason
				final[i].FilesSkippedLastRun = dl.FilesSkippedLastRun
				final[i].LastSeenUnix = dl.LastSeenUnix
				delete(degraded, final[i].Language)
			}
		}
		for _, dl := range degraded {
			final = append(final, *dl)
		}
	}

	if err := ix.store.SetDegradedLanguages(final); err != nil {
		// SPEC-142 D17: failing to WRITE the mark is a real error of the run —
		// a graph that cannot record its own incompleteness must not report
		// itself as a successful pass.
		return fmt.Errorf("codegraph: indexer: write degraded languages: %w", err)
	}
	return nil
}

// indexFile handles the incremental check, extraction, and store write for a
// single source file. It updates result in-place.
//
// indexFile itself is UNCHANGED by SPEC-142 (D8): it still returns
// ErrExtractorIncompatible exactly as SPEC-088 defined it — that sentinel is
// the signal, not the policy. What changed is entirely in the three callers
// above (via indexEligibleFile): they now register the degradation and
// continue instead of aborting the whole run.
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
		if errors.Is(err, ErrExtractorIncompatible) {
			// Systemic (SPEC-088 D4): the extractor's toolchain is present but
			// unusable, so no file of this language can be extracted. Return
			// the error so the caller aborts the walk instead of treating this
			// as an ordinary per-file failure — see the asymmetry documented
			// on Index.
			return err
		}
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
	// DeleteNodesByFile cascades to edges and unresolved_refs via FK constraints,
	// so stale cross-file unresolved refs originating from this file are cleaned up.
	if _, delErr := ix.store.DeleteNodesByFile(relPath); delErr != nil {
		return fmt.Errorf("delete nodes for %s: %w", relPath, delErr)
	}
	if uErr := ix.store.BatchUpsertNodes(extraction.Nodes); uErr != nil {
		return fmt.Errorf("batch upsert nodes for %s: %w", relPath, uErr)
	}
	if uErr := ix.store.BatchUpsertEdges(extraction.Edges); uErr != nil {
		return fmt.Errorf("batch upsert edges for %s: %w", relPath, uErr)
	}
	if uErr := ix.store.BatchUpsertUnresolvedRefs(extraction.UnresolvedRefs); uErr != nil {
		return fmt.Errorf("batch upsert unresolved refs for %s: %w", relPath, uErr)
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
// files are renamed, deleted, or moved into a directory that is now ignored.
//
// Two passes are performed:
//
//  1. Files pass: iterates the files table and removes both the file record and
//     all associated nodes for any path that is no longer on disk. This is the
//     normal case for deleted or renamed files.
//
//  2. Orphan-nodes pass: iterates the distinct file_path values in the nodes
//     table and removes nodes whose path is not in onDisk and has no entry in
//     the files table. These orphan nodes arise when a directory is added to
//     ignoredDirs after it was indexed (so the files table entry was already
//     pruned by a previous run but the nodes were not cleaned up), or when a
//     prior indexFile call succeeded in writing nodes but failed before writing
//     the file record.
func (ix *Indexer) pruneDeleted(onDisk map[string]struct{}) error {
	// Pass 1: clean up via files table (normal deleted-file flow).
	stored, err := ix.store.ListFiles()
	if err != nil {
		return fmt.Errorf("codegraph: indexer: list files: %w", err)
	}

	// Build the set of paths still present in the files table so the orphan
	// pass can skip them (they are already handled here).
	inFilesTable := make(map[string]struct{}, len(stored))
	for _, fr := range stored {
		inFilesTable[fr.Path] = struct{}{}
		if _, exists := onDisk[fr.Path]; !exists {
			if _, err := ix.store.DeleteNodesByFile(fr.Path); err != nil {
				return fmt.Errorf("codegraph: indexer: delete nodes for deleted file %s: %w", fr.Path, err)
			}
			if err := ix.store.DeleteFile(fr.Path); err != nil {
				return fmt.Errorf("codegraph: indexer: delete file record %s: %w", fr.Path, err)
			}
		}
	}

	// Pass 2: purge orphan nodes — nodes whose file_path is absent from both
	// onDisk and the files table. This covers directories that were added to
	// ignoredDirs after an initial index (the files table entry is gone, but
	// the nodes linger) and atomicity gaps where nodes were written but the
	// file record was not.
	nodePaths, err := ix.store.ListDistinctNodeFilePaths()
	if err != nil {
		return fmt.Errorf("codegraph: indexer: list node file paths: %w", err)
	}

	for _, p := range nodePaths {
		if _, onDiskNow := onDisk[p]; onDiskNow {
			continue // file is still being indexed — leave it alone
		}
		if _, inFiles := inFilesTable[p]; inFiles {
			continue // already handled in pass 1
		}
		// Orphan: node path not on disk and not in files table.
		if _, err := ix.store.DeleteNodesByFile(p); err != nil {
			return fmt.Errorf("codegraph: indexer: delete orphan nodes for %s: %w", p, err)
		}
	}

	return nil
}
