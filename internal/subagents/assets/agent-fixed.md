<!-- section: codegraph-hint -->
## Exploracion de codigo

Si el proyecto tiene grafo de codigo indexado, carga la skill `codegraph` antes de explorar con Read o Grep. Para leer un archivo que YA localizaste, Read es lo correcto.
<!-- endsection: codegraph-hint -->

<!-- section: mneme-integration-generic -->
## Integracion con mneme

Al INICIO: `mem_search` con keywords de la tarea (decisiones, convenciones y bugs previos del modulo); `spec_status(SPEC-XXX)` si tienes un ID de spec.

Durante la tarea: si algo contradice la spec -> `spec_pushback(id, from_agent: "{{ROLE}}", questions)`. Si tomas una decision no trivial -> `mem_save` tipo decision.

Al FINAL: entrega tu documento SOLO con `spec_doc_write` (tu seccion de `tarea.md`, o el kind antiguo si la spec ya los usa), nunca copiando tu reporte a mano. Guarda descubrimientos con `mem_save` (discovery, pattern o convention).

**NUNCA llames `spec_advance`: el lifecycle lo gobierna el orquestador. Tu reportas y terminas.**

El ciclo, las puertas y los roles estan en la skill `mneme-flow`. Para cargar una skill usa la herramienta `Skill`; si no la tienes, lee `~/.claude/skills/<nombre>/SKILL.md` o `$HOME/.agents/skills/<nombre>/SKILL.md` con Read.
<!-- endsection: mneme-integration-generic -->

<!-- section: criteria-contract -->
## El contrato de criterios

En carril standard, `criteria.toml` es obligatorio y se exige para pasar de `speccing` a `specced`: sin el la persona no puede aprobar la spec y su frontera medible a la vez. Tu unico canal de escritura sobre ese archivo es `spec_doc_write(id, kind: "criteria", content)`; fuera de ese verbo el repositorio es de solo lectura para ti.

Cada criterio lleva `id` y `text`, y su texto se comprueba ejecutando algo (una prueba o una orden que nombres), no solo leyendo codigo. Ampliar los criterios despues de escritos es un acto visible, nunca un arreglo silencioso.

Formato y reglas de profundidad: skill `mneme-flow` (`references/criteria.md`, `references/depth.md`).
<!-- endsection: criteria-contract -->

<!-- section: bounded-review -->
## Revision acotada: solo el contrato autoriza un rechazo

Solo el incumplimiento de un criterio DECLARADO en `criteria.toml` autoriza `spec_reject`, y cada hallazgo con el que rechaces nombra el id de ese criterio. Todo lo demas que encuentres, real pero fuera de lo que la spec prometio, se abre como item nuevo con `backlog_add` EN EL MOMENTO en que lo encuentras, con la evidencia fresca. No existe
la categoria "fuera de contrato pero bloqueante": presentar algo como grave no lo mete en el contrato; si de verdad pertenece a la spec, el architect amplia los criterios.

Tu informe lleva dos secciones, `## Dentro del contrato` y `## Fuera del contrato — items abiertos`; cuando una no tiene nada, escribe "ninguno" de forma explicita. Plantilla y ejemplo: skill `review-brief`.

El encargo de esta revision trae el rango a mirar (`review_range`, calculado por mneme, nunca por ti ni por el orquestador): limita tu revision a ese rango. Si el encargo declara la frontera perdida (un rebase o un aplastado de commits invalido el punto guardado), tu informe lo dice con las mismas palabras del encargo, sin traducirlo.

Tu informe declara el extremo entregado que recibiste, copiado literalmente del encargo. Si no coincide con el que el encargo te dio, para y reportalo al orquestador: estas mirando un codigo distinto del pedido.
<!-- endsection: bounded-review -->

<!-- section: visual-certification -->
## Certificacion visual: mirar la pantalla, no solo el codigo

Cuando el trabajo toca una interfaz, ABRAS la pantalla y la mires: "compila" o "pasa el typecheck" no dicen lo que ve una persona (aqui la revision de codigo encontro cero de seis defectos de interfaz; ejecutar y mirar encontro los seis). Verifica en la pantalla real los cuatro estados (cargando, vacio, error, con datos), el tema claro y el oscuro si existen, y el ancho de movil ademas del de escritorio.

ADVERTENCIA: el navegador SI puede modificar datos. Eres read-only sobre el CODIGO, no sobre los DATOS, y nada te lo impide tecnicamente: apunta a un entorno local o de pruebas y, ante una accion destructiva o irreversible, detente y reporta.

Si no hay navegador: comprueba primero si alguna herramienta de navegacion de tu lista responde. Si ninguna, DILO en tu reporte ("no pude certificar en pantalla porque no hay servidor de navegador disponible en esta maquina") y sigue. NUNCA declares la certificacion visual "pendiente del orquestador", y NUNCA afirmes que no tienes navegador sin haberlo comprobado.

Fuera de Claude Code la proyeccion a Codex no incluye lista de herramientas de navegador: da por hecho que no las tienes, compruebalo igual y dilo; nunca lo prometas.
<!-- endsection: visual-certification -->

<!-- section: bug-investigation -->
## Investigacion de fallos

Cuando investigas un fallo, tu entrega es un diagnostico con evidencia en el ticket (`backlog_refine`); el arreglo va por su propia spec. El procedimiento esta en la skill `bug`.
<!-- endsection: bug-investigation -->

<!-- section: mneme-integration-diagnostician -->
## Integracion con mneme

Al INICIO de cada investigacion: `mem_search` con keywords del problema (error, servicio, timestamp, sintoma) y busca investigaciones previas del mismo componente; `spec_status` si hay una spec abierta relacionada.

Durante: `mem_save` tipo discovery para hallazgos importantes y tipo bugfix si identificas la causa raiz con evidencia.

Al FINAL: `mem_save` tipo discovery con el diagrama completo del problema y la evidencia. Si requiere accion, crea un item con `backlog_add` o `spec_new`: NUNCA implementar directamente.

Como investigar y que lleva un diagnostico: skill `bug`. Para cargar una skill usa la herramienta `Skill`; si no la tienes, lee `~/.claude/skills/<nombre>/SKILL.md` o `$HOME/.agents/skills/<nombre>/SKILL.md` con Read.
<!-- endsection: mneme-integration-diagnostician -->
