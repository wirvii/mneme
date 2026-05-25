# CodeGraph — Grafo semántico de código integrado en mneme

**Fecha:** 2026-05-25
**Estado:** Draft
**Inspirado en:** [colbymchenry/codegraph](https://github.com/colbymchenry/codegraph)

## Resumen

Replicar codegraph en Go como módulo interno de mneme. El code graph indexa una base de código, extrae símbolos (funciones, structs, clases, interfaces, imports) y sus relaciones (calls, contains, implements, imports), y los persiste en una DB SQLite separada con FTS5. El agente consulta el grafo vía MCP tools para entender la estructura del código sin leer archivos completos, reduciendo consumo de tokens.

## Motivación

Los agentes AI gastan la mayor parte de sus tokens explorando código fuente: leyendo archivos, buscando con grep, navegando imports. Un grafo pre-indexado permite consultas como "¿quién llama a esta función?", "¿qué impacta si cambio este struct?", o "dame el contexto completo de este símbolo" — todo sin leer un solo archivo.

Codegraph reporta ~57% menos tokens y ~71% menos tool calls. La meta es obtener resultados similares integrados en mneme.

## Decisiones de diseño

### D1. DB separada, colocada junto a la DB de memorias

El code graph vive en `~/.mneme/projects/<slug>-codegraph.db`, junto a `<slug>.db` (memorias). Razones:

- Un repo mediano genera 5K-50K nodos — mezclarlos con el grafo de memorias (~500 entities) degradaría PPR, communities, y Hebbian.
- Schema optimizado para código (signature, visibility, line/col, decorators) que no tiene sentido en entities de memoria.
- Portable: se puede borrar y re-generar sin afectar memorias.

### D2. Parsing: go/ast para Go, Node.js subprocess para TS/JS

- **Go**: `go/ast` + `go/parser` + `go/types` (stdlib). Precisión perfecta, zero deps.
- **TypeScript/JS**: Script `extract.js` embebido vía `embed.FS`, ejecutado como subprocess Node.js. Usa el compilador oficial `typescript` para AST. Protocolo JSONL (una línea por archivo). Requiere Node.js ≥18; si no está, se emite warning y se saltan archivos TS/JS.

No se usa tree-sitter. Cada lenguaje usa su parser nativo/oficial para máxima precisión.

### D3. Paquete monolítico `internal/codegraph/`

Un solo paquete con archivos bien nombrados (~10 archivos). Sigue el patrón de `internal/vault/` y `internal/rules/`. Se subdivide solo si crece a 10+ lenguajes.

### D4. Frontends: MCP + CLI en v1

10 MCP tools (réplica de codegraph) + CLI subcommands bajo `mneme codegraph`. HTTP queda para v2.

### D5. Indexado manual e incremental

El agente o usuario ejecuta `mneme codegraph index`. No hay file watcher. El indexador compara `content_hash` (SHA256) de cada archivo — solo re-parsea los que cambiaron. Archivos eliminados eliminan sus nodos/edges vía CASCADE.

### D6. Bridge ligero con mneme

Cross-query por string matching entre entities de memoria (kind=file/module) y nodos del code graph (file_path/qualified_name). No duplica datos, no modifica schemas existentes.

### D7. Lenguajes v1: Go + TypeScript/JavaScript

Extensiones soportadas: `.go`, `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`.

## Schema SQLite

Réplica del schema de codegraph, adaptado a convenciones Go/mneme.

### Tabla `nodes`

```sql
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    file_path TEXT NOT NULL,
    language TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    start_column INTEGER NOT NULL,
    end_column INTEGER NOT NULL,
    docstring TEXT,
    signature TEXT,
    visibility TEXT,
    is_exported INTEGER DEFAULT 0,
    is_async INTEGER DEFAULT 0,
    is_static INTEGER DEFAULT 0,
    is_abstract INTEGER DEFAULT 0,
    decorators TEXT,
    type_parameters TEXT,
    updated_at INTEGER NOT NULL
);
```

### Tabla `edges`

```sql
CREATE TABLE IF NOT EXISTS edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    target TEXT NOT NULL,
    kind TEXT NOT NULL,
    metadata TEXT,
    line INTEGER,
    col INTEGER,
    provenance TEXT DEFAULT NULL,
    FOREIGN KEY (source) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target) REFERENCES nodes(id) ON DELETE CASCADE
);
```

### Tabla `files`

```sql
CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    language TEXT NOT NULL,
    size INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    indexed_at INTEGER NOT NULL,
    node_count INTEGER DEFAULT 0,
    errors TEXT
);
```

### Tabla `unresolved_refs`

```sql
CREATE TABLE IF NOT EXISTS unresolved_refs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_node_id TEXT NOT NULL,
    reference_name TEXT NOT NULL,
    reference_kind TEXT NOT NULL,
    line INTEGER NOT NULL,
    col INTEGER NOT NULL,
    candidates TEXT,
    file_path TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'unknown',
    FOREIGN KEY (from_node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
```

### Tabla `project_metadata`

```sql
CREATE TABLE IF NOT EXISTS project_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
```

### FTS5 + triggers de sincronización

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    id,
    name,
    qualified_name,
    docstring,
    signature,
    content='nodes',
    content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS nodes_ai AFTER INSERT ON nodes BEGIN
    INSERT INTO nodes_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;

CREATE TRIGGER IF NOT EXISTS nodes_ad AFTER DELETE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
END;

CREATE TRIGGER IF NOT EXISTS nodes_au AFTER UPDATE ON nodes BEGIN
    INSERT INTO nodes_fts(nodes_fts, rowid, id, name, qualified_name, docstring, signature)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.name, OLD.qualified_name, OLD.docstring, OLD.signature);
    INSERT INTO nodes_fts(rowid, id, name, qualified_name, docstring, signature)
    VALUES (NEW.rowid, NEW.id, NEW.name, NEW.qualified_name, NEW.docstring, NEW.signature);
END;
```

### Índices

```sql
CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name ON nodes(qualified_name);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_language ON nodes(language);
CREATE INDEX IF NOT EXISTS idx_nodes_file_line ON nodes(file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_nodes_lower_name ON nodes(lower(name));

CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_source_kind ON edges(source, kind);
CREATE INDEX IF NOT EXISTS idx_edges_target_kind ON edges(target, kind);
CREATE INDEX IF NOT EXISTS idx_edges_provenance ON edges(provenance);

CREATE INDEX IF NOT EXISTS idx_files_language ON files(language);
CREATE INDEX IF NOT EXISTS idx_files_modified_at ON files(modified_at);

CREATE INDEX IF NOT EXISTS idx_unresolved_from_node ON unresolved_refs(from_node_id);
CREATE INDEX IF NOT EXISTS idx_unresolved_name ON unresolved_refs(reference_name);
CREATE INDEX IF NOT EXISTS idx_unresolved_file_path ON unresolved_refs(file_path);
CREATE INDEX IF NOT EXISTS idx_unresolved_from_name ON unresolved_refs(from_node_id, reference_name);
```

## Modelo Go

### Node kinds (22 — réplica de codegraph)

```go
type NodeKind string

const (
    NodeFile        NodeKind = "file"
    NodeModule      NodeKind = "module"
    NodeClass       NodeKind = "class"
    NodeStruct      NodeKind = "struct"
    NodeInterface   NodeKind = "interface"
    NodeTrait       NodeKind = "trait"
    NodeProtocol    NodeKind = "protocol"
    NodeFunction    NodeKind = "function"
    NodeMethod      NodeKind = "method"
    NodeProperty    NodeKind = "property"
    NodeField       NodeKind = "field"
    NodeVariable    NodeKind = "variable"
    NodeConstant    NodeKind = "constant"
    NodeEnum        NodeKind = "enum"
    NodeEnumMember  NodeKind = "enum_member"
    NodeTypeAlias   NodeKind = "type_alias"
    NodeNamespace   NodeKind = "namespace"
    NodeParameter   NodeKind = "parameter"
    NodeImport      NodeKind = "import"
    NodeExport      NodeKind = "export"
    NodeRoute       NodeKind = "route"
    NodeComponent   NodeKind = "component"
)
```

### Edge kinds (12 — réplica de codegraph)

```go
type EdgeKind string

const (
    EdgeContains     EdgeKind = "contains"
    EdgeCalls        EdgeKind = "calls"
    EdgeImports      EdgeKind = "imports"
    EdgeExports      EdgeKind = "exports"
    EdgeExtends      EdgeKind = "extends"
    EdgeImplements   EdgeKind = "implements"
    EdgeReferences   EdgeKind = "references"
    EdgeTypeOf       EdgeKind = "type_of"
    EdgeReturns      EdgeKind = "returns"
    EdgeInstantiates EdgeKind = "instantiates"
    EdgeOverrides    EdgeKind = "overrides"
    EdgeDecorates    EdgeKind = "decorates"
)
```

### Structs principales

```go
type Node struct {
    ID             string   `json:"id"`
    Kind           NodeKind `json:"kind"`
    Name           string   `json:"name"`
    QualifiedName  string   `json:"qualified_name"`
    FilePath       string   `json:"file_path"`
    Language       string   `json:"language"`
    StartLine      int      `json:"start_line"`
    EndLine        int      `json:"end_line"`
    StartColumn    int      `json:"start_column"`
    EndColumn      int      `json:"end_column"`
    Docstring      string   `json:"docstring,omitempty"`
    Signature      string   `json:"signature,omitempty"`
    Visibility     string   `json:"visibility,omitempty"`
    IsExported     bool     `json:"is_exported"`
    IsAsync        bool     `json:"is_async"`
    IsStatic       bool     `json:"is_static"`
    IsAbstract     bool     `json:"is_abstract"`
    Decorators     []string `json:"decorators,omitempty"`
    TypeParameters []string `json:"type_parameters,omitempty"`
    UpdatedAt      int64    `json:"updated_at"`
}

type Edge struct {
    ID         int64    `json:"id,omitempty"`
    Source     string   `json:"source"`
    Target     string   `json:"target"`
    Kind       EdgeKind `json:"kind"`
    Metadata   string   `json:"metadata,omitempty"`
    Line       int      `json:"line,omitempty"`
    Col        int      `json:"col,omitempty"`
    Provenance string   `json:"provenance,omitempty"`
}

type FileRecord struct {
    Path        string `json:"path"`
    ContentHash string `json:"content_hash"`
    Language    string `json:"language"`
    Size        int64  `json:"size"`
    ModifiedAt  int64  `json:"modified_at"`
    IndexedAt   int64  `json:"indexed_at"`
    NodeCount   int    `json:"node_count"`
    Errors      string `json:"errors,omitempty"`
}

type UnresolvedRef struct {
    ID            int64  `json:"id,omitempty"`
    FromNodeID    string `json:"from_node_id"`
    ReferenceName string `json:"reference_name"`
    ReferenceKind string `json:"reference_kind"`
    Line          int    `json:"line"`
    Col           int    `json:"col"`
    FilePath      string `json:"file_path"`
    Language      string `json:"language"`
    Candidates    string `json:"candidates,omitempty"`
}
```

### Interfaz Extractor

```go
type ExtractionResult struct {
    Nodes          []Node          `json:"nodes"`
    Edges          []Edge          `json:"edges"`
    UnresolvedRefs []UnresolvedRef `json:"unresolved_refs"`
    Errors         []ExtractionError `json:"errors"`
    DurationMs     int64           `json:"duration_ms"`
}

type ExtractionError struct {
    Message  string `json:"message"`
    FilePath string `json:"file_path,omitempty"`
    Line     int    `json:"line,omitempty"`
    Col      int    `json:"col,omitempty"`
    Severity string `json:"severity"`
    Code     string `json:"code,omitempty"`
}

type Extractor interface {
    Extract(filePath string, content []byte) (*ExtractionResult, error)
    Language() string
}
```

## Extractores

### Extractor Go (`extractor_go.go`)

Usa `go/ast` + `go/parser` de la stdlib. Para cada archivo `.go`:

1. `parser.ParseFile` con `parser.ParseComments`
2. Recorre el AST:
   - `*ast.FuncDecl` → kind=function (sin receiver) o method (con receiver)
   - `*ast.GenDecl` con `token.TYPE` → struct, interface, o type_alias según el inner type spec
   - `*ast.GenDecl` con `token.VAR` → variable
   - `*ast.GenDecl` con `token.CONST` → constant
   - `*ast.ImportSpec` → import
3. Edges:
   - `contains`: file→func, file→type, struct→method
   - `calls`: `ast.CallExpr` dentro de function bodies → resuelve al package.func llamado
   - `imports`: file→package importado
   - `implements`: detección vía `go/types` (struct satisface interface)
4. Signature: construida desde `FuncType.Params` + `FuncType.Results`
5. `is_exported`: `ast.IsExported(name)`
6. Docstring: `FuncDecl.Doc` o `GenDecl.Doc`
7. Receiver type para methods: `FuncDecl.Recv` → qualified_name = `ReceiverType.MethodName`

### Extractor TypeScript/JS (`extractor_ts.go` + `extract.js`)

**Lado Go** (`extractor_ts.go`):
- En la primera invocación, escribe `extract.js` (embedded) a un temp dir
- Lanza `node extract.js` como subprocess con stdin/stdout pipes
- Envía rutas de archivos por stdin (batch), lee `ExtractionResult` JSON por stdout (una línea por archivo)
- Timeout de 10 segundos por archivo
- Si Node.js no está instalado, retorna error descriptivo

**Lado Node.js** (`extract.js`):
- Usa `typescript` (el compilador oficial) para parsear cada archivo
- Recorre el AST de TypeScript extrayendo:
  - `FunctionDeclaration`, `ArrowFunction`, `FunctionExpression` → function
  - `ClassDeclaration` → class
  - `MethodDeclaration` → method
  - `InterfaceDeclaration` → interface
  - `EnumDeclaration` → enum
  - `TypeAliasDeclaration` → type_alias
  - `VariableStatement` → variable/constant
  - `ImportDeclaration` → import
  - `ExportDeclaration` / `ExportAssignment` → export
- Edges: contains, calls, imports, exports, extends, implements
- Detecta JSX/TSX components (funciones que retornan JSX → kind=component)
- Emite JSONL por stdout

## MCP Tools (10 — réplica de codegraph)

Todos los tools se registran en `allTools` de `internal/mcp/tools.go`. Los handlers viven en `internal/mcp/handlers.go`. Total de tools en mneme pasa de 23 a 33.

### codegraph_search

Búsqueda FTS5 de símbolos por nombre.

**Input:**
```json
{
  "query": "string — requerido",
  "kind": "string[] — filtro por node kind",
  "language": "string[] — filtro por lenguaje",
  "limit": "int — default 20, max 50"
}
```

**Output:** Lista de nodos con score de relevancia, agrupados por archivo.

### codegraph_context

Contexto compuesto de un símbolo: nodo focal + ancestors + children + callers + callees + types + imports. Una sola llamada reemplaza 3-4 tool calls individuales.

**Input:**
```json
{
  "symbol": "string — nombre o qualified_name",
  "depth": "int — profundidad de traversal, default 1"
}
```

**Output:** Nodo focal con su contexto completo formateado.

### codegraph_callers

Traversal incoming: ¿quién llama/usa este símbolo?

**Input:**
```json
{
  "symbol": "string — requerido",
  "depth": "int — default 1, max 5",
  "limit": "int — default 20, max 100"
}
```

**Output:** Árbol de callers con profundidad, archivo, y línea del call site.

### codegraph_callees

Traversal outgoing: ¿a quién llama/usa este símbolo?

**Input:**
```json
{
  "symbol": "string — requerido",
  "depth": "int — default 1, max 5",
  "limit": "int — default 20, max 100"
}
```

**Output:** Árbol de callees.

### codegraph_impact

Radio de impacto transitivo: ¿qué se rompe si cambio este símbolo? Traversal incoming recursivo.

**Input:**
```json
{
  "symbol": "string — requerido",
  "depth": "int — default 3, max 10",
  "limit": "int — default 50, max 200"
}
```

**Output:** Lista de nodos impactados, agrupados por profundidad y archivo.

### codegraph_node

Detalle completo de un símbolo: todos los campos del nodo + source code inline (lee el archivo y extrae las líneas start_line..end_line).

**Input:**
```json
{
  "symbol": "string — requerido"
}
```

**Output:** Nodo con todos sus campos + bloque de código fuente.

### codegraph_explore

Multi-símbolo: busca varios símbolos, lee sus secciones de código, y construye un mapa de relaciones entre ellos. Budget-capped para controlar output size.

**Input:**
```json
{
  "symbols": "string[] — requerido, 1-10 símbolos",
  "budget": "int — max caracteres de output, default 30000"
}
```

**Output:** Secciones de código agrupadas por archivo + mapa de relaciones.

### codegraph_trace

Encuentra el camino de llamadas entre dos símbolos usando BFS.

**Input:**
```json
{
  "from": "string — símbolo origen",
  "to": "string — símbolo destino",
  "max_depth": "int — default 5, max 10"
}
```

**Output:** Secuencia de nodos y edges del camino, con source inline en cada paso.

### codegraph_status

Estado del índice: conteos por kind/language, última indexación, tamaño de DB, archivos con errores.

**Input:** Ninguno.

**Output:** Estadísticas del grafo en formato tabla.

### codegraph_files

Árbol de archivos indexados, opcionalmente filtrado por patrón o lenguaje.

**Input:**
```json
{
  "pattern": "string — glob pattern opcional",
  "language": "string — filtro por lenguaje"
}
```

**Output:** Árbol de archivos con conteo de nodos por archivo.

## CLI Commands

Subcomandos bajo `mneme codegraph`, registrados como `newCodegraphCmd()` en `internal/cli/codegraph.go`:

```
mneme codegraph index [path]      — indexar proyecto (default: cwd)
    --language string              — forzar lenguaje (auto-detect por defecto)
    --force                        — re-indexar todo ignorando hashes
    --dry-run                      — reportar sin escribir

mneme codegraph status             — estado del índice

mneme codegraph search <query>     — buscar símbolos
    --kind string                  — filtrar por node kind
    --language string              — filtrar por lenguaje
    --limit int                    — max resultados (default 20)

mneme codegraph callers <symbol>   — quién llama a un símbolo
    --depth int                    — profundidad (default 1)

mneme codegraph callees <symbol>   — a quién llama un símbolo
    --depth int                    — profundidad (default 1)

mneme codegraph impact <symbol>    — radio de impacto
    --depth int                    — profundidad (default 3)

mneme codegraph node <symbol>      — detalle de un símbolo

mneme codegraph trace <from> <to>  — camino entre dos símbolos
    --max-depth int                — profundidad máxima BFS (default 5)

mneme codegraph files [pattern]    — archivos indexados
    --language string              — filtrar por lenguaje
```

## Bridge con mneme

### Mecanismo

Cuando el agente busca memorias con `mem_search` y un resultado tiene entities de tipo `file` o `module`:

1. El bridge abre la codegraph DB del mismo proyecto
2. Busca `nodes.file_path = entity.name` o `nodes.qualified_name LIKE '%' || entity.name || '%'`
3. Si hay match, anota el resultado de búsqueda con `code_context_available: true`
4. El agente puede entonces invocar `codegraph_context` con ese path/nombre

### codegraph_context con memory_id

Param opcional `memory_id`. Si se pasa:

1. Carga la memoria desde mneme
2. Extrae file paths y module names mencionados en el contenido
3. Busca cada uno en el code graph
4. Devuelve el contexto compuesto de todos los matches

## Estructura de archivos

```
internal/codegraph/
  model.go            — Node, Edge, NodeKind, EdgeKind, FileRecord, etc.
  db.go               — Open, Close, schema embedded, InitSchema
  store.go            — CRUD nodes/edges/files, FTS5, traversal queries
  indexer.go          — Scan dir, detect language, dispatch, persist, incremental
  extractor.go        — interface Extractor, registry, language detection
  extractor_go.go     — go/ast parser
  extractor_ts.go     — Node.js subprocess management
  resolver.go         — Post-extraction reference resolution
  query.go            — Callers, Callees, Impact (BFS), Trace (BFS path)
  bridge.go           — Cross-query mneme entities ↔ code nodes

internal/codegraph/js/
  extract.js          — Script Node.js para TS/JS (embedded via embed.FS)

internal/service/
  codegraph.go        — Orchestration: Index, Search, Context, Callers, etc.

internal/mcp/
  tools.go            — +10 codegraph_* tool definitions
  handlers.go         — +10 codegraph_* handlers

internal/cli/
  codegraph.go        — mneme codegraph subcommands
```

## Criterios de aceptación

1. `mneme codegraph index` indexa un proyecto Go y produce nodos para funciones, structs, interfaces, methods, imports con sus relaciones (contains, calls, imports, implements).
2. `mneme codegraph index` indexa archivos TS/JS (cuando Node.js está disponible) produciendo nodos equivalentes.
3. Indexado incremental: solo re-parsea archivos cuyo content_hash cambió. Archivos eliminados borran sus nodos/edges.
4. `codegraph_search` encuentra símbolos por nombre vía FTS5 con resultados rankeados.
5. `codegraph_callers` y `codegraph_callees` hacen traversal correcto a profundidad configurable.
6. `codegraph_impact` devuelve el radio de impacto transitivo de un símbolo.
7. `codegraph_context` devuelve el contexto compuesto de un símbolo en una sola llamada.
8. `codegraph_trace` encuentra el camino de llamadas entre dos símbolos vía BFS.
9. `codegraph_explore` devuelve source + relaciones de múltiples símbolos, respetando budget de caracteres.
10. `codegraph_node` devuelve detalle del nodo + source code inline.
11. `codegraph_status` y `codegraph_files` reportan estado del índice.
12. Bridge: `mem_search` anota resultados con `code_context_available` cuando hay match en code graph.
13. Respeta `.gitignore`: no indexa vendor/, node_modules/, dist/, archivos generados.
14. Output de MCP tools tiene cap de caracteres (configurable, default 30K) para no explotar contexto del agente.
15. Build, test, lint: todo verde. Tests contra SQLite in-memory.

## Dependencias nuevas

- `go/ast`, `go/parser`, `go/types`, `go/token` — stdlib, zero deps externas
- Node.js ≥18 — solo para TS/JS, opcional (graceful degradation si no está instalado)
- `typescript` npm package — dentro de `extract.js`, no es dep de Go

## Fuera de alcance (v1)

- File watcher / auto-sync
- HTTP frontend (v2)
- Lenguajes adicionales (Python, Rust, Java — v2)
- Framework route resolvers (Gin, Express — v2)
- Resolución de callbacks / dynamic dispatch (v2)
- Bridge profundo (entities compartidas entre code graph y memory graph)
