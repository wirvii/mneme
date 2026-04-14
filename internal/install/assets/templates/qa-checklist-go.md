# QA Checklist — Go Backend

## Analisis Estatico

Correr SOLO sobre los paquetes con archivos `.go` modificados (no todo el proyecto).
Identificar paquetes afectados con: `git diff --name-only | grep '\.go$' | xargs -n1 dirname | sort -u`

Para cada paquete afectado, verificar:

| Herramienta | Que detecta | Severidad |
|-------------|-------------|-----------|
| `errcheck` | Errores de retorno no manejados — un error ignorado es un bug silencioso | CRITICO |
| `nilness` | Nil pointer dereference — panic en produccion | CRITICO |
| `shadow` | Variable shadowing — asignacion que oculta otra variable, fuente de bugs sutiles | IMPORTANTE |
| `unmarshal` | Errores de unmarshal ignorados — datos corruptos que pasan silenciosamente | CRITICO |
| `closecheck` | Recursos no cerrados (files, connections) — memory leaks | IMPORTANTE |
| `ineffassign` | Asignaciones a variables que nunca se leen — codigo muerto que confunde | IMPORTANTE |
| `gosec` | Vulnerabilidades de seguridad (SQL injection, hardcoded creds, etc.) | CRITICO |

**Reglas:**
- Si `errcheck`, `nilness`, `unmarshal` o `gosec` reportan algo, es CRITICO — rechazar
- Si `shadow`, `closecheck` o `ineffassign` reportan algo, es IMPORTANTE — rechazar
- Si la herramienta no esta instalada, reportarlo como warning pero NO bloquear por eso
- Excluir carpetas `mocks/`, `*_test.go`, `vendor/`, `proto/` del analisis

## Review de Codigo Go

### JSON Tags (VERIFICAR SIEMPRE — error frecuente)
- [ ] TODO struct que se serialice a JSON tiene `json:"snake_case"` en TODOS sus campos
- [ ] Verificar: entities, DTOs, responses, cualquier struct que pase por respuestas HTTP
- [ ] No hay campos con PascalCase en serializacion

### Aislamiento de Casos de Uso (VERIFICAR SIEMPRE — error frecuente)
- [ ] Ningun UC struct recibe otro UC como dependencia
- [ ] Ningun UC llama .Execute() de otro UC
- [ ] Logica compartida entre UCs esta en funciones helper, NO en otro UC
- [ ] Los helpers son funciones puras: no tienen struct, no tienen Execute

### Handlers (VERIFICAR SIEMPRE — error frecuente)
- [ ] Cada handler llama UN SOLO caso de uso — no orquesta multiples UCs
- [ ] No hay logica de negocio en handlers (no hay if/switch sobre estado de entidades)
- [ ] Handler solo: parsea request -> llama UC -> retorna resultado

### Arquitectura
- [ ] El core NO importa adapters
- [ ] Las interfaces estan en el dominio, no en adapters
- [ ] Dependency injection explicita (no globals, no init())
- [ ] No hay imports entre modulos (comunicacion via eventos o API client)

### SQLC & Base de Datos
- [ ] Las queries estan en `query.sql`, no en codigo Go
- [ ] Se ejecuto `sqlc generate` y no hay diffs
- [ ] Las migraciones tienen UP y DOWN
- [ ] No hay JOINs entre esquemas de modulos diferentes
- [ ] No hay FKs BIGINT (id) entre schemas — FKs entre schemas deben ser VARCHAR (iid)
- [ ] Indices necesarios estan creados

### Codigo Go
- [ ] Todos los metodos de I/O reciben `context.Context`
- [ ] Se evitan punteros innecesarios
- [ ] Los errores tienen contexto (`fmt.Errorf("...: %w", err)`)
- [ ] No hay panic/recover innecesarios
- [ ] IDs publicos: ULID, internos nunca expuestos
- [ ] Borrado logico, paginacion token-based

### Seguridad Backend
- [ ] No hay SQL injection (todo via SQLC)
- [ ] No hay secrets hardcodeados
- [ ] Los permisos se validan antes de cada operacion
- [ ] No hay race conditions en operaciones concurrentes
