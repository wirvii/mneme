# mneme — Referencia de Configuración

Guía completa de todas las secciones y campos de configuración de mneme.
Para detalles sobre comportamiento del grafo de conocimiento ver [GRAPH.md](GRAPH.md).
Para el sistema de reglas ver [RULES.md](RULES.md).

---

## Índice

- [Descripción general](#descripción-general)
- [Archivo de configuración](#archivo-de-configuración)
- [Variables de entorno](#variables-de-entorno)
- [`[storage]`](#storage)
- [`[search]`](#search)
- [`[context]`](#context)
- [`[consolidation]`](#consolidation)
- [`[decay]`](#decay)
- [`[mcp]`](#mcp)
- [`[embedding]`](#embedding)
- [`[personal]`](#personal)
- [`[workflow]`](#workflow)
- [`[delegation]`](#delegation)
- [`[spec]`](#spec)
- [`[graph]`](#graph)
- [`[suggestions]`](#suggestions)
- [Recetas de ajuste](#recetas-de-ajuste)
- [Errores de validación](#errores-de-validación)
- [Preguntas frecuentes](#preguntas-frecuentes)

---

## Descripción general

La configuración de mneme se resuelve en tres capas, en orden de prioridad:

1. **Valores por defecto** — todos los campos tienen valores seguros y listos para producción.
2. **Archivo TOML** — `~/.mneme/config.toml` (si existe). Los campos presentes sobrescriben el defecto.
3. **Variables de entorno** — siempre ganan sobre el archivo. Prefijo `MNEME_*`.

Para inspeccionar la configuración resuelta con proveniencia (de dónde viene cada valor):

```bash
mneme config show
mneme config show graph
mneme config show --json
```

> **Nota para futuras specs:** Cada spec que añada campos de configuración **DEBE** agregar:
> (a) el env override en `applyEnvOverrides()` de `internal/config/config.go`,
> (b) la entrada en la función `build<Section>Origins()` correspondiente,
> (c) la fila en la tabla de esta sección del presente documento.

---

## Archivo de configuración

Ubicación por defecto: `~/.mneme/config.toml`

Si el archivo no existe, mneme usa los valores por defecto sin errores. No es necesario crear
el archivo para que mneme funcione.

Ejemplo mínimo:

```toml
[storage]
project_budget = 500

[graph]
graph_mode = "1hop"
edge_decay_rate = 0.01
```

---

## Variables de entorno

Convención de nombres:

| Sección TOML | Prefijo de env |
|---|---|
| `[storage]` | `MNEME_DATA_DIR` (campo único) |
| `[mcp]` | `MNEME_LOG_LEVEL`, `MNEME_TOOLS` |
| `[workflow]` | `MNEME_WORKFLOW_DIR` |
| `[context]` | `MNEME_RULES_BUDGET` |
| `[graph]` | `MNEME_GRAPH_*` |
| `[suggestions]` | `MNEME_SUGGESTIONS_*` |

Los valores de entorno con formato incorrecto (ej. `MNEME_GRAPH_HEBBIAN_WINDOW=abc`) se ignoran
silenciosamente y se mantiene el valor del archivo o el defecto. Si el valor parseado falla la
validación (ej. `MNEME_GRAPH_HEBBIAN_INCREMENT=2.0` > 1.0), mneme falla al arrancar.

---

## `[storage]`

Controla dónde mneme persiste sus bases de datos SQLite y los presupuestos de memoria por scope.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `data_dir` | `~/.mneme` | string | — | `MNEME_DATA_DIR` | Directorio raíz para todos los archivos de datos. Soporta expansión de `~`. |
| `project_budget` | `1000` | int | >= 1 | — | Número máximo de memorias por scope de proyecto. |
| `global_budget` | `200` | int | >= 1 | — | Número máximo de memorias en el scope global. |

---

## `[search]`

Ajusta el comportamiento de recuperación expuesto al agente via MCP.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `default_limit` | `10` | int | >= 1 | — | Resultados devueltos cuando el llamador no especifica límite. |
| `preview_length` | `300` | int | >= 1 | — | Máximo de runas mostradas en el preview de una memoria. |
| `min_relevance` | `0.01` | float | >= 0 | — | Score mínimo para que una memoria aparezca en resultados. |

---

## `[context]`

Controla cómo mneme ensambla la inyección de contexto enviada al agente antes de cada sesión.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `default_budget` | `4000` | int | >= 0 | — | Tokens máximos para memorias inyectadas cuando el llamador no especifica budget. |
| `rules_budget` | `1500` | int | >= 0 | `MNEME_RULES_BUDGET` | Tokens reservados para memorias de tipo rule. 0 desactiva la inyección de reglas. |
| `include_global` | `true` | bool | — | — | Incluir memorias de scope global en inyecciones de contexto de proyecto. |
| `global_min_importance` | `0.7` | float | [0, 1] | — | Importancia mínima para que una memoria global sea incluida. |

---

## `[consolidation]`

Configura el proceso de fondo que evalúa, deduplica y desaloja memorias para mantener las
bases de datos dentro de sus presupuestos.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `enabled` | `true` | bool | — | — | Activa o desactiva la goroutine de consolidación de fondo. |
| `interval` | `"6h"` | string | Go duration | — | Con qué frecuencia se ejecuta la consolidación (ej. `"6h"`, `"1h30m"`). |
| `retention_days` | `30` | int | >= 0 | — | Días tras los cuales memorias de baja importancia son elegibles para desalojo. |
| `dedup_threshold` | `0.92` | float | [0, 1] | — | Similitud mínima para que dos memorias se consideren duplicadas. |

---

## `[decay]`

Tasas de decaimiento diario de importancia por tipo de memoria. Una tasa de `0.01` significa
que la importancia cae ~1% por día cuando la memoria no ha sido accedida.

Tasas más altas se usan para tipos efímeros (session summaries); tasas bajas protegen
decisiones arquitectónicas de larga duración.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `architecture` | `0.005` | float | [0, 1] | — | Tasa para memorias de tipo `architecture`. |
| `decision` | `0.005` | float | [0, 1] | — | Tasa para memorias de tipo `decision`. |
| `convention` | `0.005` | float | [0, 1] | — | Tasa para memorias de tipo `convention`. |
| `pattern` | `0.01` | float | [0, 1] | — | Tasa para memorias de tipo `pattern`. |
| `preference` | `0.01` | float | [0, 1] | — | Tasa para memorias de tipo `preference`. |
| `bugfix` | `0.02` | float | [0, 1] | — | Tasa para memorias de tipo `bugfix`. |
| `discovery` | `0.02` | float | [0, 1] | — | Tasa para memorias de tipo `discovery`. |
| `config` | `0.02` | float | [0, 1] | — | Tasa para memorias de tipo `config`. |
| `session_summary` | `0.05` | float | [0, 1] | — | Tasa para memorias de tipo `session_summary` (más efímeras). |

---

## `[mcp]`

Controla el comportamiento en tiempo de ejecución del servidor MCP.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `tools` | `"all"` | string | — | `MNEME_TOOLS` | Lista de herramientas a exponer, separadas por coma, o `"all"`. |
| `log_level` | `"info"` | string | debug/info/warn/error | `MNEME_LOG_LEVEL` | Nivel de verbosidad de los logs del servidor MCP. |

---

## `[embedding]`

Controla la estrategia de embeddings de texto usada para búsqueda semántica.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `provider` | `"tfidf"` | string | tfidf/none | — | Implementación de embedder. `"none"` usa solo FTS5. |
| `dimensions` | `512` | int | >= 1 | — | Dimensionalidad del vector producido por el embedder. |

---

## `[personal]`

Configuración del ecosistema personal del usuario (archivos CLAUDE.md compartidos entre proyectos).

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `source` | `""` | string | — | — | URL git o ruta local del ecosistema personal. Vacío = desactivado. |

---

## `[workflow]`

Controla dónde se almacenan los artefactos del ciclo de vida SDD (specs, bugs, backlog).

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `dir` | `~/.mneme/workflows` | string | — | `MNEME_WORKFLOW_DIR` | Directorio raíz para artefactos de workflow. Soporta expansión de `~`. |

---

## `[delegation]`

Controla el hook de delegación que previene que el agente orquestador edite código fuente directamente.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `enabled` | `true` | bool | — | — | Activa o desactiva el enforcement de delegación. |
| `protected_paths` | `["cmd/", "internal/", ...]` | []string | — | — | Prefijos de ruta protegidos que el orquestador no puede editar. |
| `allowed_paths` | `["docs/", "*.md", ...]` | []string | — | — | Patrones glob siempre permitidos, aunque coincidan con un prefijo protegido. |

---

## `[spec]`

Controla los quality gates y el comportamiento del ciclo de vida de specs.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `auto_grill` | `true` | bool | — | — | Requiere sesión de grill antes de avanzar una spec pasado `speccing`. |
| `quality_gates.min_acceptance_criteria` | `3` | int | >= 0 | — | Número mínimo de criterios de aceptación requeridos en una spec. |
| `quality_gates.require_out_of_scope` | `true` | bool | — | — | Requiere sección explícita de "fuera de scope". |
| `quality_gates.require_dependencies` | `true` | bool | — | — | Requiere lista de dependencias en la spec. |
| `quality_gates.max_ambiguous_terms` | `0` | int | >= 0 | — | Máximo de términos ambiguos permitidos (ej. "rápido", "muchos"). |

---

## `[graph]`

Controla el grafo de conocimiento: auto-fortalecimiento hebbiano, decaimiento de aristas, expansión
1-hop/PPR en búsquedas, valores por defecto de `mem_explore`, reconstrucción del grafo y resolución
de wikilinks. Para detalles de comportamiento ver [GRAPH.md](GRAPH.md).

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `hebbian_window` | `5` | int | >= 0 | `MNEME_GRAPH_HEBBIAN_WINDOW` | Tamaño del ring buffer para co-acceso. 0 desactiva Hebbian. |
| `hebbian_increment` | `0.05` | float | [0, 1] | `MNEME_GRAPH_HEBBIAN_INCREMENT` | Delta de peso por evento de co-acceso. |
| `hebbian_initial_weight` | `0.1` | float | [0, 1] | `MNEME_GRAPH_HEBBIAN_INITIAL_WEIGHT` | Peso para relaciones nuevas creadas por Hebbian. |
| `hebbian_buffer_size` | `1000` | int | >= 0 | `MNEME_GRAPH_HEBBIAN_BUFFER_SIZE` | Capacidad del canal async. Los eventos se descartan si el buffer está lleno. |
| `edge_decay_rate` | `0.02` | float | [0, 1] | `MNEME_GRAPH_EDGE_DECAY_RATE` | Tasa diaria de decaimiento exponencial de aristas. 0 desactiva. |
| `edge_decay_after_days` | `30` | int | >= 0 | `MNEME_GRAPH_EDGE_DECAY_AFTER_DAYS` | Período de gracia (días) antes de que comience el decaimiento. |
| `expansion_enabled` | `true` | bool | — | `MNEME_GRAPH_EXPANSION_ENABLED` | Interruptor maestro para expansión de grafo en búsquedas. |
| `expansion_threshold` | `0.3` | float | [0, 1] | `MNEME_GRAPH_EXPANSION_THRESHOLD` | Peso mínimo de relación para seguirla durante expansión. |
| `expansion_fan_out_cap` | `50` | int | >= 0 | `MNEME_GRAPH_EXPANSION_FAN_OUT_CAP` | Máximo de relaciones por entidad durante expansión. |
| `expansion_seed_top_k` | `10` | int | >= 0 | `MNEME_GRAPH_EXPANSION_SEED_TOP_K` | Número de seeds top para expansión de grafo. |
| `explore_max_nodes` | `200` | int | >= 0 | `MNEME_GRAPH_EXPLORE_MAX_NODES` | Cap duro de BFS para `mem_explore`. 0 = sin cap. |
| `explore_default_depth` | `2` | int | [0, 5] | `MNEME_GRAPH_EXPLORE_DEFAULT_DEPTH` | Profundidad por defecto para `mem_explore`. |
| `explore_default_budget` | `4000` | int | >= 0 | `MNEME_GRAPH_EXPLORE_DEFAULT_BUDGET` | Budget de tokens por defecto para `mem_explore`. |
| `rebuild_min_shared` | `2` | int | >= 1 | `MNEME_GRAPH_REBUILD_MIN_SHARED` | Mínimo de entidades compartidas para relación de co-mención. |
| `rebuild_max_relations` | `50` | int | >= 1 | `MNEME_GRAPH_REBUILD_MAX_RELATIONS` | Máximo de relaciones de co-mención por memoria. |
| `wikilinks_enabled` | `true` | bool | — | `MNEME_GRAPH_WIKILINKS_ENABLED` | Parsear `[[topic_key]]` en `mem_save`/`mem_update`. |
| `wikilink_relation_weight` | `0.6` | float | [0, 1] | `MNEME_GRAPH_WIKILINK_RELATION_WEIGHT` | Peso para relaciones creadas por el parser de wikilinks. |
| `graph_mode` | `"ppr"` | string | ppr/1hop/off | `MNEME_GRAPH_MODE` | Algoritmo de expansión de grafo (`ppr`, `1hop`, o `off`). |

### Aliases de entorno (compatibilidad hacia atrás)

Los nombres legacy de SPEC-007 siguen funcionando como alias. Cuando se establecen ambos,
el nombre canónico `MNEME_GRAPH_*` tiene prioridad:

| Legacy (alias) | Canónico (prioridad) |
|---|---|
| `MNEME_EXPANSION_ENABLED` | `MNEME_GRAPH_EXPANSION_ENABLED` |
| `MNEME_EXPANSION_THRESHOLD` | `MNEME_GRAPH_EXPANSION_THRESHOLD` |
| `MNEME_EXPANSION_FAN_OUT_CAP` | `MNEME_GRAPH_EXPANSION_FAN_OUT_CAP` |
| `MNEME_EXPANSION_SEED_TOP_K` | `MNEME_GRAPH_EXPANSION_SEED_TOP_K` |

---

## `[suggestions]`

Controla el comportamiento de `mem_suggest_topic_key` para matching contra topic keys existentes
y gaps de conocimiento no resueltos. Introducido en SPEC-014.

| Field | Default | Type | Range | Env Override | Description |
|-------|---------|------|-------|-------------|-------------|
| `gap_score_boost` | `0.15` | float | [0, 1] | `MNEME_SUGGESTIONS_GAP_SCORE_BOOST` | Boost aditivo al score Jaccard de gaps. Valores altos hacen que los gaps emerjan más agresivamente. |
| `gap_pending_weight` | `0.10` | float | [0, 1] | `MNEME_SUGGESTIONS_GAP_PENDING_WEIGHT` | Multiplicador aplicado a `log2(pending_count+1)` al puntuar gaps. |
| `gap_jaccard_threshold` | `0.2` | float | [0, 1] | `MNEME_SUGGESTIONS_GAP_JACCARD_THRESHOLD` | Similitud Jaccard mínima para incluir un gap en sugerencias. |
| `max_gaps_to_consider` | `50` | int | >= 0 | `MNEME_SUGGESTIONS_MAX_GAPS_TO_CONSIDER` | Máximo de gaps top a evaluar por similitud Jaccard. |
| `max_results` | `10` | int | >= 1 | `MNEME_SUGGESTIONS_MAX_RESULTS` | Máximo de sugerencias totales devueltas. |

---

## Recetas de ajuste

### Receta 1: Proyecto pequeño (< 100 memorias)

Con pocos elementos, ventanas grandes y caps altos son ruido. Reducirlos mejora la precisión.

```toml
[graph]
hebbian_window = 3          # ventana pequeña evita ruido con pocas memorias
expansion_seed_top_k = 5    # menos seeds, más rápido
explore_max_nodes = 50      # grafo pequeño, no se necesitan 200
rebuild_min_shared = 1      # K=1 es razonable con pocas memorias (menos superposición)
```

### Receta 2: Proyecto grande (> 1000 memorias, grafo denso)

Grafos densos necesitan más capacidad de buffer y exploración más profunda.

```toml
[graph]
hebbian_buffer_size = 5000  # más espacio para eventos async
expansion_fan_out_cap = 100 # explorar más del grafo denso
expansion_seed_top_k = 20   # más seeds para mejor cobertura
explore_max_nodes = 500     # exploraciones más profundas
rebuild_min_shared = 3      # K=3 para evitar co-menciones ruidosas
rebuild_max_relations = 100 # permitir más relaciones por memoria
```

### Receta 3: CI / máquina de baja capacidad

Desactivar el procesamiento de grafo para mínimo overhead en entornos restringidos.

```toml
[graph]
graph_mode = "off"          # omitir expansión de grafo completamente
hebbian_window = 0          # desactivar Hebbian (sin trabajo de fondo)
edge_decay_rate = 0         # desactivar decaimiento de aristas
```

O vía variables de entorno sin modificar el archivo:

```bash
MNEME_GRAPH_MODE=off MNEME_GRAPH_HEBBIAN_WINDOW=0 MNEME_GRAPH_EDGE_DECAY_RATE=0 mneme mcp
```

### Receta 4: Modo debug (máxima visibilidad)

Para diagnóstico y trazado de comportamiento del grafo.

```toml
[mcp]
log_level = "debug"

[graph]
graph_mode = "1hop"         # más simple, más fácil de trazar
expansion_threshold = 0.1   # seguir relaciones débiles también
explore_default_depth = 5   # profundidad máxima
explore_max_nodes = 1000    # ver todo

[consolidation]
enabled = false             # suspender consolidación durante debug
```

---

## Errores de validación

mneme falla al arrancar con un mensaje claro si la configuración no es válida. Errores comunes:

| Error | Causa | Solución |
|-------|-------|----------|
| `storage.data_dir must not be empty` | `data_dir = ""` en el archivo o `MNEME_DATA_DIR=""` | Especificar una ruta válida |
| `storage.project_budget must be greater than 0` | Budget <= 0 | Usar un valor >= 1 |
| `mcp.log_level "verbose" is not valid` | Nivel no reconocido | Usar debug/info/warn/error |
| `graph.edge_decay_rate must be in [0.0, 1.0]` | Tasa > 1.0 o negativa | Usar [0.0, 1.0]; 0 desactiva decay |
| `graph.hebbian_increment must be in [0.0, 1.0]` | Incremento fuera de rango | Usar [0.0, 1.0] |
| `graph.graph_mode "invalid" is not valid` | Modo desconocido | Usar ppr/1hop/off |
| `graph.explore_default_depth must be between 0 and 5` | Profundidad > 5 | Usar [0, 5] |
| `suggestions.max_results must be >= 1` | max_results = 0 | Usar >= 1 |

Los valores de variable de entorno con formato incorrecto (no parseables) se ignoran
silenciosamente. Un valor parseable pero fuera de rango (ej. `MNEME_GRAPH_EDGE_DECAY_RATE=2.0`)
falla la validación y detiene el arranque.

---

## Preguntas frecuentes

**¿Debo crear un archivo config.toml?**
No. Todos los campos tienen valores por defecto seguros. mneme funciona sin archivo de configuración.

**¿Cómo veo qué valor está activo para cada campo?**
```bash
mneme config show           # todos los campos con proveniencia
mneme config show graph     # solo la sección [graph]
mneme config show --json    # salida machine-readable
```

**¿Las variables de entorno persisten entre sesiones?**
No. Son por-proceso. Para valores persistentes usa el archivo `~/.mneme/config.toml`.

**¿Puedo usar `expansion_enabled = false` y `graph_mode = "ppr"` juntos?**
Sí. `expansion_enabled = false` es el interruptor absoluto: desactiva toda expansión de grafo
independientemente de `graph_mode`. `graph_mode` solo importa cuando `expansion_enabled = true`.

**¿Qué pasa si env y archivo TOML tienen valores diferentes para el mismo campo?**
El env gana. La prioridad es siempre: env > archivo > defecto.

**¿Puedo ver qué env var controla un campo específico?**
```bash
mneme config show graph --json | grep "graph_mode" -A4
# muestra "env_var": "MNEME_GRAPH_MODE"
```
