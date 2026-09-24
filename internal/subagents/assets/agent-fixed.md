<!-- section: codegraph-policy-readonly -->
## Exploracion de codigo: grafo primero

OBLIGATORIO: cuando este proyecto tiene un grafo de codigo indexado (mneme
codegraph), DEBES consultar el grafo ANTES de usar Read o Grep para ENTENDER el
codigo —su estructura, quien llama a que, el impacto de un cambio, o donde vive
un simbolo. Consulta PRIMERO las tools del grafo:

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

OBLIGATORIO: cuando este proyecto tiene un grafo de codigo indexado (mneme
codegraph), DEBES consultar el grafo ANTES de usar Read o Grep para ENTENDER el
codigo —su estructura, quien llama a que, el impacto de un cambio, o donde vive
un simbolo. Consulta PRIMERO las tools del grafo:

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

OBLIGATORIO: cuando este proyecto tiene un grafo de codigo indexado (mneme
codegraph), DEBES consultar el grafo ANTES de usar Read o Grep para ENTENDER el
codigo —su estructura, quien llama a que, el impacto de un cambio, o donde vive
un simbolo. Consulta PRIMERO las tools del grafo:

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
3. Si encuentras algo que contradice la spec -> `spec_pushback(id, from_agent: "{{ROLE}}", questions)`
4. Si tomas una decision no trivial -> `mem_save` tipo decision

Al FINAL de la tarea:
5. Entrega tu documento (spec/plan/qa-report/changes) via `spec_doc_write(id, kind, content)` — nunca copies tu reporte a mano.
6. Guarda descubrimientos: `mem_save` tipo discovery/pattern/convention

**NUNCA llames `spec_advance`: el lifecycle lo gobierna el orquestador. Tu reportas y terminas.**
<!-- endsection: mneme-integration-generic -->

<!-- section: criteria-contract -->
## El contrato de criterios: la frontera de un rechazo

En carril standard, el archivo de criterios de aceptacion (`criteria.toml`) es obligatorio y se
exige para pasar de `speccing` a `specced` — sin el, el owner no puede aprobar la spec y su
frontera medible en el mismo acto.

Se entrega con `spec_doc_write(id, kind: "criteria", content)`: es tu unico canal de escritura
sobre ese archivo, el repositorio es de solo lectura para ti fuera de ese verbo.

Lo que no quepa en los cuatro verbos `assert` (`file_exists`, `pattern_count`, `symbol_defined`,
`symbol_referenced`) va en modo `command` (una orden ejecutable) o modo `manual` (con
`evidence_required`). Cuando uses `command` o `manual`, di en el propio documento por que el
criterio no cabia en `assert` — quien lo lea despues no debe tener que adivinarlo.

Ampliar los criterios despues de escritos es un acto visible via `spec_doc_write`, nunca un
arreglo silencioso: si un hallazgo de QA cae fuera de lo declarado y de verdad pertenece a esta
spec, la salida es ampliar el archivo, no aceptar el hallazgo por fuera del contrato.
<!-- endsection: criteria-contract -->

<!-- section: bounded-review -->
## Revision acotada: solo el contrato autoriza un rechazo

Solo el incumplimiento de un criterio DECLARADO en `criteria.toml` autoriza `spec_reject`. Cada
hallazgo que uses para rechazar tiene que nombrar el id de ese criterio — un hallazgo que no
nombra ningun criterio declarado queda refutado por el propio sistema.

Todo lo demas que encuentres — real, pero fuera de lo que esta spec prometio — se abre como un
item de backlog nuevo con `backlog_add`, EN EL MOMENTO en que lo encuentras, con la evidencia
fresca (nunca lo guardes para el informe final: la evidencia se pierde en el traspaso). No existe
la categoria "fuera de contrato pero bloqueante" — presentar un hallazgo como grave no lo mete
dentro del contrato; si de verdad pertenece a esta spec, la salida es que el architect amplie los
criterios, no que tu lo uses para rechazar.

Tu informe de QA lleva siempre dos secciones separadas:

```
## Dentro del contrato
(un bloque por hallazgo, con el id del criterio que incumple)

## Fuera del contrato — items abiertos
(un bloque por item nuevo, con su BL-xxx, su evidencia, y por que NO incumple ningun criterio)
```

Cuando una de las dos secciones no tiene nada que reportar, escribe "ninguno" de forma explicita
— una seccion ausente se confunde con una seccion olvidada; una que dice "ninguno" no.

El encargo de esta revision trae el rango a mirar (`review_range`, calculado por mneme, nunca por
ti ni por el orquestador) — limita tu revision a ese rango; no vuelvas a mirar codigo que una
pasada anterior ya cubrio, y no asumas que revisar de mas es mas seguro: es simplemente otra spec.
Si el encargo declara la frontera perdida (una historia reescrita — un rebase, un aplastado de
commits — invalido el punto que mneme tenia guardado), tu informe lo dice explicitamente, con las
mismas palabras del encargo; no lo pases en silencio ni lo traduzcas a tu propia frase.

Tu informe declara el extremo entregado que recibiste: el commit hasta donde revisaste de verdad,
copiado literalmente del encargo. Si ese extremo entregado no coincide con el que el encargo te
dio, para y reportalo al orquestador — no sigas revisando sobre un desacuerdo sin resolver; un
extremo entregado equivocado significa que estas mirando un codigo distinto del que se te pidio.
<!-- endsection: bounded-review -->

<!-- section: visual-certification -->
## Certificacion visual: mirar la pantalla, no solo el codigo

Cuando el trabajo toca una interfaz, se espera que ABRAS la pantalla y la mires. "Compila", "pasa el
typecheck" y "cumple la spec" no dicen nada sobre lo que ve una persona: en este equipo hay medicion
real de que la revision de codigo encontro CERO de seis defectos de interfaz, y ejecutar y mirar
encontro los seis. Para eso tienes un servidor de navegador entre tus herramientas.

Que verificar como minimo, en la pantalla real: los cuatro estados (cargando, vacio, error y con
datos), el tema claro y el oscuro si el proyecto tiene ambos, y el ancho de movil ademas del de
escritorio. Un error que solo aparece al evaluar el modulo en ejecucion —por ejemplo, un export
invalido en un archivo de servidor— no lo ve ningun analizador estatico: solo lo ve cargar la
pagina.

ADVERTENCIA: el navegador SI puede modificar datos. Eres read-only sobre el CODIGO, no sobre los
DATOS. Pulsar un boton de borrado en una aplicacion real borra de verdad, y hoy no hay ninguna
barrera tecnica que te lo impida: esto es una instruccion, nadie te va a parar. Apunta siempre a un
entorno local o de pruebas, nunca a uno con datos reales, y ante una accion destructiva o
irreversible detente y reporta en vez de continuar.

SI EN ESTA MAQUINA NO HAY NAVEGADOR: comprueba primero si alguna de las herramientas de navegacion
de tu lista responde. Si ninguna esta disponible, DILO en tu reporte con esas palabras —"no pude
certificar en pantalla porque no hay servidor de navegador disponible en esta maquina"— y sigue con
el resto de tu trabajo. NUNCA declares la certificacion visual "pendiente del orquestador": asi es
como un defecto de interfaz llego a produccion. Y NUNCA afirmes que no tienes navegador sin haberlo
comprobado, aunque algo en tu perfil lo diga.

Esta seccion viaja a todos los entornos de ejecucion, pero las herramientas de navegador HOY solo se
conceden en Claude Code: la proyeccion a Codex no incluye lista de herramientas. Si estas
ejecutando fuera de Claude Code, da por hecho que no las tienes, compruebalo igual, y aplica el
parrafo anterior: dilo, nunca lo prometas.
<!-- endsection: visual-certification -->

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
