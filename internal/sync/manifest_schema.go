package sync

import "embed"

// schemaFS embeds the JSON Schema 2020-12 files that define the Memory Manifest
// v1.0 interchange format. The schemas are bundled into the tarball produced by
// ManifestExporter so consumers can validate archives offline without network access.
//
//go:embed schemas/*.schema.json
var schemaFS embed.FS
