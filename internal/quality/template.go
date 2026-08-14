package quality

// templateTOML is the exact content mneme init writes to a repository's
// .mneme/quality.toml when the file is absent (D15/AC23/AC32). Every key
// Parse requires is present and uncommented (schema_version 6, enabled,
// execution.output_tail_bytes, and the complete [coverage]/[ratchet]/
// [criteria]/[budget]/[mutation]/[visual]/[visual.compare] tables, since
// schema 6 requires all six sections in full) so the written file parses
// without error the moment it lands; every `enabled` switch is false so
// materializing the constitution never itself starts blocking spec_advance
// in a repo that never asked for it (R4) — and, since SPEC-118 D12, turning
// [budget] on ALSO starts requiring a certificate for the trivial lane, so
// this template leaves it off by construction. The example gates stay
// commented out — they name the shape a team declares, without inventing
// gates mneme cannot know are correct for this project (D9 of the grill).
// [coverage]/[ratchet]/[criteria]/[budget]/[mutation]/[visual]/
// [visual.compare], by contrast, CANNOT be left commented out (schema 6
// requires all six sections present with every key), so their values are a
// generic, harmless illustration — never executed while enabled=false.
const templateTOML = `# .mneme/quality.toml — constitución de calidad de este repositorio.
# Versionada y revisable en PR: la calidad es parte del código.
# mneme NO tiene valores por defecto para nada de este fichero.

schema_version = 6

# El interruptor. Mientras sea false el mecanismo NO bloquea nada.
# Ponerlo a true es un commit revisable, y volverlo a false dentro del rango
# de commits de una spec es un BLOQUEO, no un aviso (ver docs/quality.md).
enabled = false

[execution]
# Bytes de la cola de salida que se guardan por gate (1..65536).
output_tail_bytes = 4096

# Declara aquí los gates que build+prueban tu proyecto, en el orden en que
# deben ejecutarse. Ejemplo:
#
# [[gate]]
# name = "build"
# command = ["make", "build"]
# timeout = "5m"
# required = true
#
# [[gate]]
# name = "test"
# command = ["make", "test"]
# timeout = "20m"
# required = true

[coverage]
# La cobertura de las lineas AÑADIDAS O MODIFICADAS por una spec (SPEC-116).
# false = declarado apagado, a proposito. A diferencia de los gates, esta
# tabla no puede quedar comentada: el schema 2 la exige completa.
enabled = false

# Formato del perfil de cobertura: "lcov" | "go-cover". Declarado, nunca
# adivinado (ver docs/quality.md).
format = "lcov"

# El comando que produce el perfil de cobertura, ejecutado tal cual, sin
# shell — ajusta esto al toolchain real de tu proyecto antes de encender
# coverage.enabled.
command = ["true"]

# Donde deja el comando el perfil, relativo a la raiz del repo. mneme LO
# BORRA antes de ejecutar el comando: debe estar en .gitignore.
profile_path = "coverage.lcov"

timeout = "20m"

# Umbral de cobertura de las lineas cambiadas por la spec.
min_diff_line_pct = 80.0

# Por debajo de este numero de lineas elegibles la comprobacion se salta.
min_changed_lines = 5

# Ficheros excluidos del numerador Y del denominador (globs doublestar).
exclude = []

[ratchet]
# El cliquet: la cobertura global del repositorio no puede caer sin firma.
# Requiere coverage.enabled = true.
enabled = false

# Caida tolerada, en puntos porcentuales, respecto a la linea base.
max_global_line_pct_drop = 0.0

# Cuanto puede la medicion superar la marca registrada antes de declararla
# obsoleta (ver docs/quality.md). Debe ser >= max_global_line_pct_drop.
max_baseline_staleness_pct = 1.0

[criteria]
# Los criterios de aceptacion ejecutables (SPEC-117). false = declarado
# apagado, a proposito, en un commit revisable.
enabled = false

# Cota de la fase estructurada completa (git ls-tree + git grep sobre los
# dos refs). No cubre los criterios ` + "`command`" + `: cada uno declara el suyo.
timeout = "5m"

# Cupo de criterios MANUALES, en porcentaje del total declarado. Superarlo
# hace FALLAR el certificado: el problema ya no es la verificacion, es que
# los criterios estan mal escritos (D14 del grill).
# Aritmetica con N pequeno: con 4 criterios, 25.0 permite exactamente 1.
max_manual_pct = 25.0

# Cupo de criterios que usan la ESCOTILLA de comando libre. Existe por el
# mismo motivo: sin el, la escotilla se traga el vocabulario cerrado.
max_command_pct = 30.0

[budget]
# El presupuesto contra el grafo (SPEC-118). false = declarado apagado, a
# proposito. ATENCION: encenderlo activa TAMBIEN el certificado para la
# lane TRIVIAL (ver docs/lanes.md) — a partir de ahi ` + "`mneme lane audit`" + `
# exige certificado.
enabled = false

# Cota de la fase de presupuesto completa: lectura de blobs, extraccion de
# simbolos y consultas al grafo. NO ejecuta ningun comando del proyecto.
timeout = "2m"

# Los ficheros que NO cuentan contra el presupuesto y que SI cuentan como
# "llamador de test" para la deteccion solo-tests. UNA sola lista para las
# dos cosas.
test_globs = ["**/*_test.go", "**/*.test.ts", "**/*.test.tsx", "**/*.spec.ts", "**/*.spec.tsx"]

# Cuantos saltos de indireccion se aceptan como "un test lo alcanza".
test_reach_depth = 3

[mutation]
# La mutacion sobre el diff (SPEC-119). false = declarado apagado, a
# proposito. Correr un mutador ejecuta la suite del proyecto una vez POR
# MUTANTE — es la comprobacion mas cara del EPIC. Ver docs/quality.md
# antes de encenderlo.
enabled = false

# Formato del informe de mutantes: "gremlins" | "mutants-v1". Declarado,
# nunca adivinado — un formato mal declarado produce cero mutantes y un
# VERDE que no demuestra nada.
format = "mutants-v1"

# El comando que produce el informe, ejecutado tal cual, sin shell. El
# token {{BASE_SHA}} (opcional) se sustituye por la merge-base que mneme
# calcula, si tu mutador sabe acotarse a un rango — mneme reacota el
# informe por su cuenta de todos modos, asi que omitir el token solo
# cuesta tiempo, nunca correccion.
command = ["true"]

# Donde deja el comando el informe, relativo a la raiz del repo. mneme LO
# BORRA antes de ejecutar: debe estar en .gitignore.
report_path = "tmp/mutants.json"

# Presupuesto de la fase completa de mutacion.
timeout = "30m"

# Cupo ABSOLUTO (nunca porcentual) de mutantes que un qa-tester puede
# firmar como equivalentes en un mismo certificado. 0 = sin escotilla.
max_equivalent = 2

# Por encima de esta proporcion de mutantes NO VIABLES (los que ni
# siquiera compilan) el informe habla del mutador y no de los tests.
max_not_viable_pct = 25.0

[visual]
# SPEC-120 (S6): la verificacion visual declarativa. false = declarado
# apagado, a proposito. NO exige que el proyecto tenga interfaz grafica: un
# repositorio sin una puede declarar esta seccion completa y apagada, con
# targets = [].
enabled = false

# Formato del informe. Hoy solo "visual-v1". Declarado, nunca adivinado.
format = "visual-v1"

# El comando que levanta la interfaz, la recorre y deja el informe. mneme
# lo ejecuta tal cual, sin shell, y ESPERA a que termine: el ciclo de vida
# del servidor es del comando, nunca de mneme.
command = ["true"]

# Donde el comando deja el informe, relativo a la raiz del repo. mneme LO
# BORRA antes de ejecutar: debe estar en .gitignore.
report_path = "tmp/visual/report.json"

# Cota de la fase completa: arrancar, recorrer y apagar.
timeout = "15m"

# QUE se verifica. Identificadores OPACOS: mneme no interpreta ni un
# caracter. Ruta, estado, tema y ancho son doctrina del PROYECTO. Con
# enabled = true, la lista vacia es un ERROR.
targets = []

# true convierte cualquier console.error en fallo. Las excepciones no
# capturadas fallan SIEMPRE, con independencia de esta clave.
fail_on_console_error = false

# Impactos de accesibilidad que hacen fallar. Vocabulario cerrado:
# critical | serious | moderate | minor. Vacio = se mide y se registra,
# pero no bloquea.
a11y_fail_impacts = []

[visual.compare]
# El NIVEL 2, separado a proposito: la comparacion pixel a pixel falla sola
# y cada falso positivo entrena a aprobar a ciegas. Quien no lo quiera, no
# lo enciende.
enabled = false

# Donde viven las referencias. VERSIONADO: mneme NUNCA escribe aqui.
reference_dir = ".mneme/visual/reference"

# Donde el comando deja las capturas. SALIDA: ignorado por git.
capture_dir = "tmp/visual/captures"

# Tolerancia, en porcentaje de pixeles distintos. La comparacion es
# estricta: con la tolerancia justa, pasa.
max_diff_pct = 0.1
`

// Template returns the exact content mneme init writes as a repository's
// starter .mneme/quality.toml. It is guaranteed to Parse without error (see
// TestTemplate_ParsesWithoutError) — a template that failed its own parser
// would defeat the entire mechanism the moment a project materialized it.
func Template() string {
	return templateTOML
}
