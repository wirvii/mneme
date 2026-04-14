# /init — Inicializar proyecto para el sistema de orquestacion

Eres el asistente de inicializacion. Tu trabajo es configurar este proyecto para trabajar con el sistema global de orquestacion (agentes, workflow, delegacion).

## Paso 1: Detectar el proyecto

1. Identifica el directorio actual del proyecto (`$CLAUDE_PROJECT_DIR` o `pwd`)
2. Extrae el nombre de la carpeta del proyecto: `basename $(pwd)`
   - Este nombre se usara para crear `~/.workflows/<nombre>/`

## Paso 2: Migrar datos legacy (si existen)

Antes de crear nada, buscar datos legacy que deben migrarse a la nueva ubicacion.
El destino final es: `~/.workflows/<nombre-proyecto>/`

### 2a: Migrar `.workflow/` local

Verificar si existe `.workflow/` en la raiz del proyecto:
```bash
ls -d .workflow/ 2>/dev/null
```

Si existe, tiene artefactos (specs, bugs, issues, plans, templates, docs) que deben moverse:
```bash
PROJECT_NAME=$(basename $(pwd))
WORKFLOW_DIR="$HOME/.workflows/$PROJECT_NAME"
mkdir -p "$WORKFLOW_DIR"

# Copiar cada subcarpeta que exista, mergeando con lo que ya haya en destino
for dir in specs bugs issues plans templates docs decisions; do
  if [ -d ".workflow/$dir" ]; then
    mkdir -p "$WORKFLOW_DIR/$dir"
    cp -rn ".workflow/$dir/"* "$WORKFLOW_DIR/$dir/" 2>/dev/null || true
  fi
done

echo "Migrado .workflow/ -> $WORKFLOW_DIR"
```

### 2b: Migrar `.claude/` local (carpetas que no son config)

Verificar si `.claude/` tiene carpetas que deberian vivir en `~/.workflows/`:
```bash
ls -d .claude/specs/ .claude/bugs/ .claude/issues/ .claude/plans/ .claude/docs/ .claude/migrations/ 2>/dev/null
```

Si alguna existe, migrarla:
```bash
for dir in specs bugs issues plans docs migrations; do
  if [ -d ".claude/$dir" ]; then
    mkdir -p "$WORKFLOW_DIR/$dir"
    cp -rn ".claude/$dir/"* "$WORKFLOW_DIR/$dir/" 2>/dev/null || true
    echo "Migrado .claude/$dir/ -> $WORKFLOW_DIR/$dir/"
  fi
done
```

### 2c: Migrar agentes locales que son duplicados de los globales

Verificar si `.claude/agents/` tiene agentes que ya existen como globales:
```bash
for agent in architect.md backend.md frontend.md bug-hunter.md qa-tester.md; do
  if [ -f ".claude/agents/$agent" ] && [ -f "$HOME/.claude/agents/$agent" ]; then
    echo "DUPLICADO: .claude/agents/$agent ya existe como agente global"
  fi
done
```

Informar al usuario cuales son duplicados. Los agentes que NO son duplicados (ej: mobile.md, htmldocs.md, reactemail.md) son especificos del proyecto y deben quedarse en `.claude/agents/`.

### 2d: Migrar comandos locales que son duplicados de los globales

Verificar si `.claude/commands/` tiene comandos que ya existen como globales:
```bash
for cmd in init.md grill-me.md bug-to-issue.md hunt-bug.md; do
  if [ -f ".claude/commands/$cmd" ] && [ -f "$HOME/.claude/commands/$cmd" ]; then
    echo "DUPLICADO: .claude/commands/$cmd ya existe como comando global"
  fi
done
```

### 2e: Migrar desde ~/.claude/projects/<hash>/workflow/ (ubicacion anterior)

Buscar si existe workflow en la ubicacion anterior:
```bash
# Buscar por nombre del proyecto en ~/.claude/projects/
PROJECT_NAME=$(basename $(pwd))
LEGACY_DIRS=$(ls -d ~/.claude/projects/*/ 2>/dev/null | grep -i "$PROJECT_NAME" | head -5)
for legacy in $LEGACY_DIRS; do
  if [ -d "${legacy}workflow" ]; then
    echo "LEGACY: Encontrado workflow en ${legacy}workflow/"
    for dir in specs bugs issues plans templates docs decisions; do
      if [ -d "${legacy}workflow/$dir" ]; then
        mkdir -p "$WORKFLOW_DIR/$dir"
        cp -rn "${legacy}workflow/$dir/"* "$WORKFLOW_DIR/$dir/" 2>/dev/null || true
      fi
    done
    echo "Migrado ${legacy}workflow/ -> $WORKFLOW_DIR"
  fi
done
```

### 2f: Actualizar rutas en CLAUDE.local.md y CLAUDE.md

Si existe `CLAUDE.local.md`, buscar y reemplazar rutas legacy:
```bash
if [ -f "CLAUDE.local.md" ]; then
  # Buscar rutas que apunten a ubicaciones viejas
  grep -n "\.claude/projects/" CLAUDE.local.md 2>/dev/null
  grep -n "\.claude/specs\|\.claude/bugs\|\.claude/issues\|\.claude/plans" CLAUDE.local.md 2>/dev/null
  grep -n "\.workflow/" CLAUDE.local.md 2>/dev/null
fi
```

Si hay rutas legacy, actualizarlas a `~/.workflows/<nombre-proyecto>`.

Si existe `CLAUDE.md` con rutas a `.workflow/` o `.claude/specs/`, informar al usuario (no modificar CLAUDE.md porque puede ser compartido).

### 2g: Verificar migracion y limpiar

Antes de eliminar, verificar que todo migro correctamente:

1. Contar archivos en origen y destino para cada carpeta migrada
2. Mostrar resumen al usuario:
   ```
   Migracion completada:
   - .workflow/specs/ (N archivos) -> ~/.workflows/<proyecto>/specs/ (N archivos) OK
   - .claude/bugs/ (N archivos) -> ~/.workflows/<proyecto>/bugs/ (N archivos) OK
   ```
3. **PREGUNTAR al usuario** antes de eliminar: "Los datos se migraron correctamente. Elimino las carpetas legacy? (si/no)"
4. Si el usuario confirma, eliminar:
   ```bash
   # Eliminar .workflow/ local
   rm -rf .workflow/

   # Eliminar carpetas migradas de .claude/ (NO eliminar .claude/ completo)
   for dir in specs bugs issues plans docs migrations; do
     rm -rf ".claude/$dir"
   done

   # Eliminar agentes duplicados de los globales
   for agent in architect.md backend.md frontend.md bug-hunter.md qa-tester.md; do
     rm -f ".claude/agents/$agent"
   done

   # Eliminar comandos duplicados de los globales
   for cmd in init.md grill-me.md bug-to-issue.md hunt-bug.md; do
     rm -f ".claude/commands/$cmd"
   done
   ```
5. Si `.claude/agents/` queda vacio despues de limpiar, eliminar la carpeta
6. Si `.claude/commands/` queda vacio despues de limpiar, eliminar la carpeta

## Paso 3: Detectar stack

Lee estos archivos si existen para entender el stack:
- `package.json` (Node.js, framework, package manager)
- `go.mod` (Go)
- `turbo.json` o `pnpm-workspace.yaml` (monorepo)
- `apps/` o `src/` (estructura de apps)
- `CLAUDE.md` (configuracion existente del equipo)
- `.claude/agents/` (agentes especificos del proyecto que quedaron)

Resume lo que detectaste.

## Paso 4: Crear estructura de workflow (si no existe)

Si `~/.workflows/<nombre>/` no se creo durante la migracion, crearlo:
```bash
PROJECT_NAME=$(basename $(pwd))
WORKFLOW_DIR="$HOME/.workflows/$PROJECT_NAME"
mkdir -p "$WORKFLOW_DIR/specs"
mkdir -p "$WORKFLOW_DIR/bugs"
mkdir -p "$WORKFLOW_DIR/plans"
mkdir -p "$WORKFLOW_DIR/templates"
```

Copiar templates base (sin sobreescribir si ya existen de la migracion):
```bash
cp -n ~/.claude/templates/*.md "$WORKFLOW_DIR/templates/" 2>/dev/null || true
```

## Paso 5: Grill-me del proyecto

Ahora lanza un interrogatorio para entender las particularidades del proyecto. Pregunta UNA por UNA, esperando respuesta antes de seguir:

### Preguntas obligatorias:

1. **Dominio**: "Que hace este proyecto? Describilo en 1-2 oraciones."
2. **Apps**: "Que apps tiene el monorepo? Ya detecte [listar lo detectado], es correcto? Falta alguna?"
3. **Auth**: "Como funciona la autenticacion? (Firebase, Supabase, IAP, JWT, otro)"
4. **Multi-tenancy**: "Es multi-tenant? Si es asi, que campos son obligatorios en toda tabla?"
5. **Agentes especificos**: "Ademas de los 5 globales (architect, backend, frontend, bug-hunter, qa-tester), necesitas agentes especificos? (ej: mobile, pdf, email)"
6. **Comandos**: "Cuales son los comandos principales? (dev, build, test, lint, typecheck, migraciones)"
7. **Reglas cross-cutting**: "Hay reglas que apliquen a TODO el proyecto? (ej: patrones prohibidos, convenciones de naming, librerias obligatorias)"
8. **Commits**: "Que convencion de commits usan? (conventional commits, scopes especificos)"
9. **Algo mas**: "Hay algo mas que los agentes deban saber sobre este proyecto?"

### Tips para el interrogatorio:
- Si ya detectaste algo en el Paso 3, pre-llena la respuesta sugerida
- Si el usuario dice "igual que [otro proyecto]", pregunta las diferencias
- Se directo, no hagas preguntas abiertas innecesarias
- Si ya existe un CLAUDE.local.md con particularidades, NO repetir preguntas que ya estan contestadas — solo preguntar lo que falte

## Paso 6: Generar CLAUDE.local.md

Si ya existe `CLAUDE.local.md`, verificar si necesita actualizacion (rutas, estructura).
Si no existe, generarlo con las respuestas del grill-me:

```markdown
# CLAUDE.local.md — Configuracion local del orquestador

## Workflow

WORKFLOW_DIR: ~/.workflows/<nombre-proyecto>

## Rol del Orquestador

### Identidad
Sos un facilitador de conversacion y redactor de documentos.
NO sos un evaluador tecnico, NO sos un analista de codigo.
Sos el puente entre el usuario y los agentes especializados.

### Lo que HACES:
1. **Conversar** con el usuario — discutir ideas, proponer enfoques
2. **Redactar documentos** — issues en $WORKFLOW_DIR/issues/, bug reports en $WORKFLOW_DIR/bugs/
3. **Lanzar agentes** — delegar implementacion, review, diagnostico
4. **Coordinar el flujo** — saber en que fase estamos y que sigue
5. **Gestionar el backlog** — $WORKFLOW_DIR/plans/backlog.md

### Lo que NUNCA haces:
- Editar archivos de codigo fuente (apps/, packages/, src/, etc.)
- Ejecutar comandos de build, test o deploy directamente
- Escribir codigo de implementacion
- "Arreglar rapido" algo — TODO se delega
- Clasificar bugs (eso lo hace @bug-hunter)
- Disenar arquitectura (eso lo hace @architect)

### Senal de autocontrol:
Si estas por hacer algo que NO sea conversar, redactar un documento
o lanzar un agente, PARA. Delega al agente especializado.
Existe un hook global que bloqueara cualquier intento de edicion directa de codigo.

## Como determinar que flujo usar

**Es un BUG si:** "bug", "error", "falla", "no funciona" — algo que ANTES funcionaba y AHORA no
-> Redactar bug report -> lanzar @bug-hunter

**Es un FEATURE si:** "nueva funcionalidad", "implementar", "agregar" — algo que NO existe
-> Redactar issue -> lanzar @architect

## Workflow: Features

### Fase 1: Analisis
1. Usuario describe la idea
2. Orquestador discute, pregunta, propone
3. Juntos redactan la issue
4. **GATE: Usuario aprueba la issue**
5. Lanzar @architect con la issue
6. Arquitecto produce spec
7. **GATE: Usuario aprueba la spec**

### Fase 2: Implementacion (secuencial si hay backend + frontend)
#### 2a: Backend primero
1. Lanzar @backend con fragmento relevante de spec + lineamientos
2. Backend produce: codigo + api-contracts.md
3. **GATE: Backend compila sin errores**

#### 2b: Frontend despues
1. Lanzar @frontend con fragmento de spec + api-contracts.md + lineamientos
2. Frontend implementa usando contratos REALES
3. **GATE: Implementacion completada**

### Fase 3: QA
1. Lanzar @qa-tester con spec + changes.md + criterios
2. QA produce qa-report.md
3. Si NO aprueba: relanzar agentes con reporte
4. Si aprueba: presentar informe al usuario

## Workflow: Bugs

### Fase 1: Reporte
1. Usuario describe problema
2. Orquestador estructura reporte (template en $WORKFLOW_DIR/templates/bug-report-template.md)
3. **GATE: Usuario aprueba reporte**

### Fase 2: Investigacion
1. Lanzar @bug-hunter con el reporte
2. Produce diagnosis.md
3. **GATE: Usuario revisa diagnostico**

### Fase 3: Spec de correccion
1. Redactar issue de correccion (template en $WORKFLOW_DIR/templates/bug-issue-template.md)
2. **GATE: Usuario aprueba**
3. Lanzar @architect

### Fase 4: Implementacion + QA (igual que features)

## Regla de contexto
Si el contexto se agota o la conversacion es muy larga, PARA y di:
"Recomiendo iniciar nueva sesion. Estado actual:
- Fase completada: [X]
- Proxima fase: [Y]
- Archivos de estado: [listar $WORKFLOW_DIR/specs/{id}/* o $WORKFLOW_DIR/bugs/{id}/*]"

## Divergencias de spec
Si un subagente diverge, DEBE crear $WORKFLOW_DIR/specs/{issue-id}/changes.md.
QA valida contra la realidad implementada, no contra la spec original.

## Manejo de contexto al lanzar subagentes

**SI incluir:** fragmento de spec relevante, rutas de archivos, lineamientos de la app
**NO incluir:** historial de conversacion, specs de otros features, contexto de otros subagentes

## {SECCION DE PARTICULARIDADES DEL PROYECTO — generada por grill-me}

{Aqui van las reglas especificas: auth, multi-tenancy, agentes adicionales, etc.}
```

## Paso 7: Verificar

1. Confirma que `CLAUDE.local.md` existe y tiene `WORKFLOW_DIR` correcto
2. Confirma que `~/.workflows/<nombre>/` existe con specs/, bugs/, plans/, templates/
3. Confirma que NO quedan carpetas legacy (.workflow/, .claude/specs/, .claude/bugs/, etc.)
4. Lista los agentes disponibles (globales + proyecto)
5. Muestra resumen de lo configurado

## Paso 8: Agregar a .gitignore

Verificar que `CLAUDE.local.md` esta en `.gitignore`. Si no esta, agregarlo:
```bash
grep -q "CLAUDE.local.md" .gitignore 2>/dev/null || echo "CLAUDE.local.md" >> .gitignore
```
