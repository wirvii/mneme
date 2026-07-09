package sync

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

// ErrUnsupportedManifestVersion is returned when a manifest archive declares a
// version that this implementation does not support. The caller must not attempt
// partial parsing — data integrity requires an explicit version upgrade path.
var ErrUnsupportedManifestVersion = errors.New("unsupported manifest version")

// ManifestImporter reads a Memory Manifest v1.0 tarball and persists all four
// data types (memories, entities, relations, sessions) into a MemoryStore.
// Each type has its own idempotency key so that importing the same archive
// twice is safe.
type ManifestImporter struct {
	s *store.MemoryStore
}

// NewManifestImporter constructs a ManifestImporter backed by s.
func NewManifestImporter(s *store.MemoryStore) *ManifestImporter {
	return &ManifestImporter{s: s}
}

// Import decompresses r as a .manifest.tar.gz archive, reads the manifest.json
// entry, and persists all records according to the following deduplication
// strategy:
//
//   - Memory with TopicKey: upsert by (topic_key, project, scope).
//   - Memory without TopicKey: skip if the ID already exists, otherwise create.
//   - Entity: skip if (name, project) already exists.
//   - Relation: skip if (source_id, target_id, type) already exists.
//     Also skips when the referenced entity IDs are not present in the DB.
//   - Session: skip if the ID already exists.
//
// Returns a ManifestImportResult summarising the operation, or an error if the
// archive cannot be read or a required version check fails.
func (imp *ManifestImporter) Import(ctx context.Context, r io.Reader) (*ManifestImportResult, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest import: create gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// The spec requires manifest.json to be the first tar entry.
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("sync: manifest import: read first tar entry: %w", err)
	}
	if hdr.Name != manifestFilename {
		return nil, fmt.Errorf("sync: manifest import: first tar entry is %q, want %q", hdr.Name, manifestFilename)
	}

	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest import: read manifest.json: %w", err)
	}

	var root ManifestRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("sync: manifest import: unmarshal manifest.json: %w", err)
	}

	if root.Version != ManifestVersion {
		return nil, fmt.Errorf("sync: manifest import: %w %q; this tool supports %q",
			ErrUnsupportedManifestVersion, root.Version, ManifestVersion)
	}

	result := &ManifestImportResult{}

	if err := imp.importMemories(ctx, root.Memories, result); err != nil {
		return nil, fmt.Errorf("sync: manifest import: memories: %w", err)
	}
	// importEntities returns a mapping from manifest entity IDs to the IDs they
	// were stored under in the destination store. This mapping is needed to remap
	// relation source/target IDs before inserting them.
	entityIDMap, err := imp.importEntities(ctx, root.Entities, result)
	if err != nil {
		return nil, fmt.Errorf("sync: manifest import: entities: %w", err)
	}
	if err := imp.importRelations(ctx, root.Relations, entityIDMap, result); err != nil {
		return nil, fmt.Errorf("sync: manifest import: relations: %w", err)
	}
	if err := imp.importSessions(ctx, root.Sessions, result); err != nil {
		return nil, fmt.Errorf("sync: manifest import: sessions: %w", err)
	}

	return result, nil
}

// importMemories upserts memories using the same strategy as the JSONL importer:
// topic_key takes precedence for dedup; fall back to ID-based skip-if-exists.
func (imp *ManifestImporter) importMemories(ctx context.Context, memories []*model.Memory, result *ManifestImportResult) error {
	seenIDs := make(map[string]bool, len(memories))

	for _, m := range memories {
		if m == nil {
			continue
		}

		// Duplicate IDs within the manifest — import first, skip rest.
		if seenIDs[m.ID] {
			log.Printf("sync: manifest import: duplicate memory ID %q — skipping", m.ID)
			result.MemoriesSkipped++
			continue
		}
		if m.ID != "" {
			seenIDs[m.ID] = true
		}

		if m.TopicKey != "" {
			_, created, err := imp.s.Upsert(ctx, m)
			if err != nil {
				return fmt.Errorf("upsert memory %s: %w", m.ID, err)
			}
			if created {
				result.MemoriesCreated++
			} else {
				result.MemoriesUpdated++
			}
			continue
		}

		// No TopicKey: skip if already exists by ID.
		if m.ID != "" {
			existing, err := imp.s.Get(ctx, m.ID)
			if err != nil {
				return fmt.Errorf("get memory %s: %w", m.ID, err)
			}
			if existing != nil {
				result.MemoriesSkipped++
				continue
			}
		}

		// Create with a new generated ID (public store API does not allow
		// caller-supplied IDs, consistent with the JSONL importer).
		m.ID = ""
		if _, err := imp.s.Create(ctx, m); err != nil {
			return fmt.Errorf("create memory: %w", err)
		}
		result.MemoriesCreated++
	}

	return nil
}

// importEntities inserts entities that do not already exist by (name, project).
// It returns a map from the manifest entity ID to the stored entity ID in the
// destination database. This mapping is required because CreateEntity generates
// a new ID — relations in the manifest reference the original IDs, which must
// be translated before insertion.
func (imp *ManifestImporter) importEntities(ctx context.Context, entities []*model.Entity, result *ManifestImportResult) (map[string]string, error) {
	// idMap: manifestID → dstID
	idMap := make(map[string]string, len(entities))

	for _, e := range entities {
		if e == nil {
			continue
		}

		manifestID := e.ID

		existing, err := imp.s.GetEntityByName(ctx, e.Name, e.Project)
		if err != nil && !errors.Is(err, model.ErrEntityNotFound) {
			return nil, fmt.Errorf("get entity %q: %w", e.Name, err)
		}
		if existing != nil {
			idMap[manifestID] = existing.ID
			result.EntitiesSkipped++
			continue
		}

		created, err := imp.s.CreateEntity(ctx, e)
		if err != nil {
			return nil, fmt.Errorf("create entity %q: %w", e.Name, err)
		}
		idMap[manifestID] = created.ID
		result.EntitiesCreated++
	}

	return idMap, nil
}

// importRelations inserts relations whose source and target entities exist in
// the destination database. The entityIDMap translates manifest entity IDs to
// the IDs they were stored under in the destination (since CreateEntity
// generates new IDs). Orphan relations (no mapping found) are logged and skipped.
func (imp *ManifestImporter) importRelations(ctx context.Context, relations []*model.Relation, entityIDMap map[string]string, result *ManifestImportResult) error {
	for _, r := range relations {
		if r == nil {
			continue
		}

		// Translate manifest IDs to destination IDs via the entity ID map.
		dstSourceID, srcOK := entityIDMap[r.SourceID]
		dstTargetID, tgtOK := entityIDMap[r.TargetID]

		if !srcOK {
			// entityIDMap miss — fall back to direct lookup in case the entity
			// already existed in the destination before this import.
			srcEntity, err := imp.s.GetEntity(ctx, r.SourceID)
			if err != nil {
				log.Printf("sync: manifest import: skip relation %q — source entity %q not found", r.ID, r.SourceID)
				result.RelationsSkipped++
				continue
			}
			dstSourceID = srcEntity.ID
		}

		if !tgtOK {
			tgtEntity, err := imp.s.GetEntity(ctx, r.TargetID)
			if err != nil {
				log.Printf("sync: manifest import: skip relation %q — target entity %q not found", r.ID, r.TargetID)
				result.RelationsSkipped++
				continue
			}
			dstTargetID = tgtEntity.ID
		}

		// Dedup: skip if the relation already exists (bidirectional check).
		existing, err := imp.s.FindRelationBidirectional(ctx, dstSourceID, dstTargetID, r.Type)
		if err != nil {
			return fmt.Errorf("find relation: %w", err)
		}
		if existing != nil {
			result.RelationsSkipped++
			continue
		}

		// Create a copy with translated IDs so the original manifest is not mutated.
		rel := *r
		rel.ID = "" // let CreateRelation generate a new ID
		rel.SourceID = dstSourceID
		rel.TargetID = dstTargetID

		if _, err := imp.s.CreateRelation(ctx, &rel); err != nil {
			return fmt.Errorf("create relation: %w", err)
		}
		result.RelationsCreated++
	}

	return nil
}

// importSessions inserts sessions that do not already exist by ID.
func (imp *ManifestImporter) importSessions(ctx context.Context, sessions []*model.Session, result *ManifestImportResult) error {
	for _, sess := range sessions {
		if sess == nil {
			continue
		}

		// GetLastSession cannot be used for ID-based lookup; use ListSessionsByProject
		// as a proxy: check if a session with this ID already exists by listing all
		// and matching. For large stores this is O(N) but sessions are typically few.
		existing, err := imp.s.GetLastSession(ctx, sess.Project)
		if err != nil {
			return fmt.Errorf("get last session for project %q: %w", sess.Project, err)
		}

		// Check all sessions for the project to find an ID match.
		if existing != nil {
			allSessions, err := imp.s.ListSessionsByProject(ctx, sess.Project)
			if err != nil {
				return fmt.Errorf("list sessions for project %q: %w", sess.Project, err)
			}
			found := false
			for _, s := range allSessions {
				if s.ID == sess.ID {
					found = true
					break
				}
			}
			if found {
				result.SessionsSkipped++
				continue
			}
		}

		if _, err := imp.s.CreateSession(ctx, sess); err != nil {
			return fmt.Errorf("create session %q: %w", sess.ID, err)
		}
		result.SessionsCreated++
	}

	return nil
}

// ImportManifestFromFile opens path, auto-detects the format, and dispatches to
// the appropriate importer. If format is manifest, uses ManifestImporter; if
// JSONL, uses the legacy Importer.
//
// Returns a ManifestImportResult for manifests, or a ManifestImportResult built
// from the JSONL ImportResult (with only memory counts populated) for JSONL.
func ImportManifestFromFile(ctx context.Context, s *store.MemoryStore, path string) (*ManifestImportResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sync: import manifest from file: open: %w", err)
	}
	defer f.Close()

	// Read a small header to detect format by content when extension is unknown.
	format := DetectFormat(path)
	if format == FormatUnknown {
		// Read up to 512 bytes for content sniffing.
		header := make([]byte, 512)
		n, readErr := f.Read(header)
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("sync: import manifest from file: read header: %w", readErr)
		}
		format = DetectFormatFromContent(header[:n])

		// Reopen the file since we consumed part of it.
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("sync: import manifest from file: close for reopen: %w", err)
		}
		f, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("sync: import manifest from file: reopen: %w", err)
		}
	}

	switch format {
	case FormatManifest:
		imp := NewManifestImporter(s)
		return imp.Import(ctx, f)

	case FormatJSONL:
		imp := NewImporter(s)
		jsonlResult, err := imp.Import(ctx, f)
		if err != nil {
			return nil, fmt.Errorf("sync: import manifest from file: jsonl: %w", err)
		}
		// Convert JSONL result to ManifestImportResult (memory fields only).
		return &ManifestImportResult{
			MemoriesCreated: jsonlResult.Created,
			MemoriesUpdated: jsonlResult.Updated,
			MemoriesSkipped: jsonlResult.Skipped,
		}, nil

	default:
		return nil, fmt.Errorf("sync: import manifest from file: cannot determine format for %q", path)
	}
}
