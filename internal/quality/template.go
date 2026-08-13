package quality

// templateTOML is the exact content mneme init writes to a repository's
// .mneme/quality.toml when the file is absent (D15/AC23). Every key Parse
// requires is present and uncommented (schema_version, enabled,
// execution.output_tail_bytes) so the written file parses without error the
// moment it lands; enabled is false so materializing the constitution never
// itself starts blocking spec_advance in a repo that never asked for it
// (R4). The two example gates are commented out — they name the shape a
// team declares, without inventing gates mneme cannot know are correct for
// this project (D9 of the grill: mneme stays agnostic of the project's
// toolchain).
const templateTOML = `# .mneme/quality.toml — constitución de calidad de este repositorio.
# Versionada y revisable en PR: la calidad es parte del código.
# mneme NO tiene valores por defecto para nada de este fichero.

schema_version = 1

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
`

// Template returns the exact content mneme init writes as a repository's
// starter .mneme/quality.toml. It is guaranteed to Parse without error (see
// TestTemplate_ParsesWithoutError) — a template that failed its own parser
// would defeat the entire mechanism the moment a project materialized it.
func Template() string {
	return templateTOML
}
