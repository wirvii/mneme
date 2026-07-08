<!-- section: codegraph-policy-readonly -->
## Exploracion de codigo: grafo primero

Este proyecto puede tener un grafo de codigo indexado (mneme codegraph). Antes de
usar Read o Grep para ENTENDER el codigo —su estructura, quien llama a que, el
impacto de un cambio, o donde vive un simbolo— usa PRIMERO las tools del grafo:

- `codegraph_search`   — encontrar simbolos por nombre o concepto
- `codegraph_context`  — vecindario de un simbolo (definicion + relaciones)
- `codegraph_callers`  — quien llama a un simbolo
- `codegraph_callees`  — a quien llama un simbolo
- `codegraph_impact`   — que se ve afectado si cambias un simbolo
- `codegraph_trace`    — caminos entre dos simbolos

Cae a Read/Grep SOLO si: el grafo no cubre la pregunta, esta desactualizado
(stale), o el repo no esta indexado. Para leer el contenido literal de un archivo
que YA localizaste, Read es lo correcto.

Aviso de cobertura: `codegraph_search`, `codegraph_context` y `codegraph_callers`
son fiables. En cambio `codegraph_impact` y `codegraph_callees` pueden estar
INCOMPLETOS: el grafo no capta de forma fiable method-calls (`x.Foo()`) ni
llamadas cross-package/stdlib. Para un analisis de impacto EXHAUSTIVO antes de un
refactor, complementa con `Grep`/`Read` — no asumas que "nadie llama a X" solo
porque el grafo no lo muestre.
<!-- endsection: codegraph-policy-readonly -->

<!-- section: codegraph-policy-implementer -->
## Exploracion de codigo: grafo primero

Este proyecto puede tener un grafo de codigo indexado (mneme codegraph). Antes de
usar Read o Grep para ENTENDER el codigo —su estructura, quien llama a que, el
impacto de un cambio, o donde vive un simbolo— usa PRIMERO las tools del grafo:

- `codegraph_search`   — encontrar simbolos por nombre o concepto
- `codegraph_context`  — vecindario de un simbolo (definicion + relaciones)
- `codegraph_callers`  — quien llama a un simbolo
- `codegraph_callees`  — a quien llama un simbolo
- `codegraph_impact`   — que se ve afectado si cambias un simbolo
- `codegraph_trace`    — caminos entre dos simbolos

Cae a Read/Grep SOLO si: el grafo no cubre la pregunta, esta desactualizado
(stale), o el repo no esta indexado. Para leer el contenido literal de un archivo
que YA localizaste, Read es lo correcto.

Aviso de cobertura: `codegraph_search`, `codegraph_context` y `codegraph_callers`
son fiables. En cambio `codegraph_impact` y `codegraph_callees` pueden estar
INCOMPLETOS: el grafo no capta de forma fiable method-calls (`x.Foo()`) ni
llamadas cross-package/stdlib. Para un analisis de impacto EXHAUSTIVO antes de un
refactor, complementa con `Grep`/`Read` — no asumas que "nadie llama a X" solo
porque el grafo no lo muestre.

NO uses `Bash` (grep/cat/find/rg/head/tail) para navegar o entender la estructura
del codigo: eso lo resuelven las tools del grafo y Read/Grep nativos. Reserva Bash
para build, test, git y operaciones —no para explorar codigo.
<!-- endsection: codegraph-policy-implementer -->

<!-- section: codegraph-policy-diagnostician -->
## Exploracion de codigo: grafo primero

Este proyecto puede tener un grafo de codigo indexado (mneme codegraph). Antes de
usar Read o Grep para ENTENDER el codigo —su estructura, quien llama a que, el
impacto de un cambio, o donde vive un simbolo— usa PRIMERO las tools del grafo:

- `codegraph_search`   — encontrar simbolos por nombre o concepto
- `codegraph_context`  — vecindario de un simbolo (definicion + relaciones)
- `codegraph_callers`  — quien llama a un simbolo
- `codegraph_callees`  — a quien llama un simbolo
- `codegraph_impact`   — que se ve afectado si cambias un simbolo
- `codegraph_trace`    — caminos entre dos simbolos

Cae a Read/Grep SOLO si: el grafo no cubre la pregunta, esta desactualizado
(stale), o el repo no esta indexado. Para leer el contenido literal de un archivo
que YA localizaste, Read es lo correcto.

Aviso de cobertura: `codegraph_search`, `codegraph_context` y `codegraph_callers`
son fiables. En cambio `codegraph_impact` y `codegraph_callees` pueden estar
INCOMPLETOS: el grafo no capta de forma fiable method-calls (`x.Foo()`) ni
llamadas cross-package/stdlib. Para un analisis de impacto EXHAUSTIVO antes de un
refactor, complementa con `Grep`/`Read` — no asumas que "nadie llama a X" solo
porque el grafo no lo muestre.

NO uses `Bash` (grep/cat/find/rg) para navegar o entender la estructura del CODIGO
—usa las tools del grafo. Bash sigue siendo tu herramienta para leer LOGS, infra y
diagnostico operacional (ver `## Permisos de Bash`): esa exploracion no cambia.
<!-- endsection: codegraph-policy-diagnostician -->

<!-- section: mneme-integration-generic -->
## Integracion con mneme

Al INICIO de cada tarea:
1. Llama `mem_search` con keywords del feature/bug para encontrar:
   - Decisiones arquitectonicas previas relevantes
   - Convenciones del proyecto
   - Bugs anteriores en el mismo modulo
   - Patrones establecidos
2. Lee el estado de la spec: `spec_status(SPEC-XXX)` si tienes un ID de spec

Durante la tarea:
3. Si encuentras algo que contradice la spec -> `spec_pushback(id, from_agent, questions)`
4. Si tomas una decision no trivial -> `mem_save` tipo decision

Al FINAL de la tarea:
5. Avanza el estado: `spec_advance(SPEC-XXX, by: "{{ROLE}}")`
6. Guarda descubrimientos: `mem_save` tipo discovery/pattern/convention
<!-- endsection: mneme-integration-generic -->

<!-- section: mneme-integration-diagnostician -->
## Integracion con mneme

Al INICIO de cada investigacion:
1. `mem_search` con keywords del problema (error, servicio, timestamp, sintoma)
2. Buscar investigaciones previas del mismo sistema/componente
3. `spec_status` si hay una spec abierta relacionada

Durante la investigacion:
4. `mem_save` tipo discovery para hallazgos importantes
5. `mem_save` tipo bugfix si identificas el root cause con evidencia

Al FINAL:
6. `mem_save` tipo discovery con el diagrama completo del problema y evidencia
7. Si requiere accion: crear backlog item via `backlog_add` o `spec_new` — NUNCA implementar directamente
<!-- endsection: mneme-integration-diagnostician -->
