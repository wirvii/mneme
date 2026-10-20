#!/usr/bin/env bash
# skill-comply (SPEC-172): mide si un agente real sigue el flujo SDD de mneme.
#
# Construye mneme, prepara un HOME y un repositorio de prueba aislados, lanza
# `claude -p` con una tarea por escenario y comprueba de forma determinista el
# estado de mneme, el árbol del repositorio y la transcripción.
#
# Cuesta suscripción de Claude en cada corrida real. Se ejecuta a mano antes de
# publicar; NUNCA en CI ni en `make test`. Guía completa: docs/skill-comply.md.
#
# Códigos de salida: 0 todas las comprobaciones duras pasan; 1 alguna falla
# (incumplimiento del agente); 2 fallo del arnés (no es culpa del agente).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# El HOME real se captura antes de cualquier cambio: sirve para comprobar que
# el arnés jamás escribe en el ~/.mneme de verdad.
REAL_HOME="$HOME"
ALL_SCENARIOS=(feature bug trivial present-spec)

SCENARIO="all"
LABEL=""
BINARY=""
CLAUDE_OPT=""
MODEL=""
MAX_TURNS=40
TIMEOUT=900
OUT=""
KEEP=0
DRY_RUN=0
SELF_TEST=0
PREFLIGHT=1

ALLOWED_TOOLS="mcp__mneme,Skill,Read,Grep,Glob,Edit,Write,MultiEdit,Task,Bash(go test:*),Bash(go build:*),Bash(go vet:*),Bash(go run:*),Bash(git status:*),Bash(git diff:*),Bash(git add:*),Bash(git commit:*),Bash(git log:*),Bash(mneme:*)"

usage() {
  cat <<'EOF'
Uso: scripts/skill-comply/run.sh [opciones]

  --scenario feature|bug|trivial|present-spec|all   (por defecto: all)
  --label <texto>        nombre de la corrida (por defecto run-AAAAMMDD-HHMMSS)
  --binary <ruta mneme>  usa este mneme en vez de compilar el árbol actual
  --claude <ruta>        CLI de claude (si no: $SKILL_COMPLY_CLAUDE o el PATH)
  --model <alias>        modelo para claude
  --max-turns N          turnos máximos por escenario (por defecto 40)
  --timeout SEG          tiempo límite por escenario (por defecto 900)
  --out DIR              salidas (por defecto <repo>/tmp/skill-comply/<label>)
  --keep                 conserva work/ (HOME aislado y repo de prueba)
  --dry-run              prepara todo e imprime la orden claude sin ejecutarla
  --self-test            autopruebas sin claude ni HOME aislado
  --no-preflight         omite la comprobación previa de autenticación
EOF
}

die_harness() {
  echo "skill-comply: $*" >&2
  exit 2
}

need_arg() {
  [[ $# -ge 2 ]] || die_harness "falta el valor de $1"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) need_arg "$@"; SCENARIO="$2"; shift 2 ;;
    --label) need_arg "$@"; LABEL="$2"; shift 2 ;;
    --binary) need_arg "$@"; BINARY="$2"; shift 2 ;;
    --claude) need_arg "$@"; CLAUDE_OPT="$2"; shift 2 ;;
    --model) need_arg "$@"; MODEL="$2"; shift 2 ;;
    --max-turns) need_arg "$@"; MAX_TURNS="$2"; shift 2 ;;
    --timeout) need_arg "$@"; TIMEOUT="$2"; shift 2 ;;
    --out) need_arg "$@"; OUT="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    --self-test) SELF_TEST=1; shift ;;
    --no-preflight) PREFLIGHT=0; shift ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; die_harness "opción desconocida: $1" ;;
  esac
done

# ---------------------------------------------------------------- self-test

self_test() {
  local failed=0 tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  report() { # report <nombre> <0|1>
    if [[ "$2" -eq 0 ]]; then echo "PASS $1"; else echo "FAIL $1"; failed=1; fi
  }

  # (a) sin claude falla claro y no crea nada.
  local code=0 errfile="$tmp/stderr"
  bash "$SCRIPT_DIR/run.sh" --claude /nonexistent --scenario feature --out "$tmp/out" 2>"$errfile" >/dev/null || code=$?
  if [[ "$code" -eq 2 ]] && grep -q 'no encuentro la CLI de claude' "$errfile" && [[ ! -e "$tmp/out" ]]; then
    report "sin claude: exit 2, mensaje claro y nada creado" 0
  else
    report "sin claude: exit 2, mensaje claro y nada creado (exit=$code)" 1
  fi

  # (b) sintaxis de bash.
  local ok=0 f
  for f in "$SCRIPT_DIR/run.sh" "$SCRIPT_DIR"/scenarios/*/seed.sh; do
    bash -n "$f" || ok=1
  done
  report "bash -n sobre run.sh y seed.sh" "$ok"

  # (c) prompts presentes y marcador de present-spec.
  ok=0
  for f in "${ALL_SCENARIOS[@]}"; do
    [[ -s "$SCRIPT_DIR/scenarios/$f/prompt.md" ]] || ok=1
  done
  grep -q '{{SPEC_ID}}' "$SCRIPT_DIR/scenarios/present-spec/prompt.md" || ok=1
  report "los 4 prompt.md existen y present-spec lleva {{SPEC_ID}}" "$ok"

  # (d) el verificador sobre sus transcripciones enlatadas.
  ok=0
  (cd "$REPO_ROOT" && go run ./scripts/skill-comply/check selftest --dir scripts/skill-comply/check/testdata) || ok=1
  report "check selftest sobre testdata/" "$ok"

  return "$failed"
}

if [[ "$SELF_TEST" -eq 1 ]]; then
  self_test
  exit $?
fi

# ------------------------------------------------------------ requisitos (D3)

CLAUDE=""
if [[ "$DRY_RUN" -eq 0 ]]; then
  candidate="${CLAUDE_OPT:-${SKILL_COMPLY_CLAUDE:-}}"
  if [[ -z "$candidate" ]]; then
    candidate="$(command -v claude || true)"
    [[ -n "$candidate" ]] || candidate="claude"
  fi
  if [[ ! -x "$candidate" ]]; then
    die_harness "no encuentro la CLI de claude (probé: $candidate). Instala Claude Code o usa --self-test / --dry-run."
  fi
  CLAUDE="$candidate"
else
  CLAUDE="${CLAUDE_OPT:-${SKILL_COMPLY_CLAUDE:-claude}}"
fi

case "$SCENARIO" in
  all) SCENARIOS=("${ALL_SCENARIOS[@]}") ;;
  feature | bug | trivial | present-spec) SCENARIOS=("$SCENARIO") ;;
  *) die_harness "escenario desconocido: $SCENARIO (válidos: feature, bug, trivial, present-spec, all)" ;;
esac

[[ "$MAX_TURNS" =~ ^[0-9]+$ ]] || die_harness "--max-turns debe ser un entero"
[[ "$TIMEOUT" =~ ^[0-9]+$ ]] || die_harness "--timeout debe ser un entero"

[[ -n "$LABEL" ]] || LABEL="run-$(date +%Y%m%d-%H%M%S)"
[[ -n "$OUT" ]] || OUT="$REPO_ROOT/tmp/skill-comply/$LABEL"
mkdir -p "$OUT/bin"
OUT="$(cd "$OUT" && pwd)"
BIN_DIR="$OUT/bin"

# ------------------------------------------------------------ compilación

# La caché de Go se toma del HOME real: el HOME aislado empieza vacío y
# recompilar todo en cada escenario sería lento.
GOCACHE_REAL="$(go env GOCACHE)"
GOMODCACHE_REAL="$(go env GOMODCACHE)"

if [[ -n "$BINARY" ]]; then
  [[ -x "$BINARY" ]] || die_harness "--binary no es ejecutable: $BINARY"
  cp "$BINARY" "$BIN_DIR/mneme"
else
  (cd "$REPO_ROOT" && go build -o "$BIN_DIR/mneme" ./cmd/mneme) || die_harness "falló la compilación de mneme"
fi
(cd "$REPO_ROOT" && go build -o "$BIN_DIR/check" ./scripts/skill-comply/check) || die_harness "falló la compilación del verificador"
CHECK="$BIN_DIR/check"

# --------------------------------------------------------- aislamiento (D4/D8)

# isolate_env deja el entorno del proceso actual apuntando al HOME aislado y
# sin ninguna variable de mneme o del cliente heredada. Solo se llama dentro de
# un subshell por escenario.
isolate_env() {
  local v
  for v in $(compgen -e); do
    case "$v" in
      MNEME_* | CLAUDE_CONFIG_DIR | CODEX_HOME) unset "$v" ;;
    esac
  done
  export HOME="$1" USERPROFILE="$1"
  export GIT_CONFIG_GLOBAL="$1/.gitconfig" GIT_TERMINAL_PROMPT=0
  export GOCACHE="$GOCACHE_REAL" GOMODCACHE="$GOMODCACHE_REAL"
  export PATH="$BIN_DIR:$PATH"
}

# list_real_mneme lista los nombres bajo el ~/.mneme REAL.
list_real_mneme() {
  local d
  for d in projects workflows; do
    find "$REAL_HOME/.mneme/$d" -mindepth 1 -maxdepth 1 -exec basename {} \; 2>/dev/null | sed "s|^|$d/|" || true
  done | sort
}

# assert_real_untouched falla (exit 2) si apareció algo de skill-comply en el
# ~/.mneme real desde que se guardó el listado inicial.
assert_real_untouched() {
  local before="$1" added
  added="$(comm -13 "$before" <(list_real_mneme) | grep -E 'skill-comply|fixture' || true)"
  if [[ -n "$added" ]]; then
    die_harness "se escribió en el ~/.mneme real ($added) — aborto"
  fi
}

# shquote entrecomilla un argumento para command.txt. No usa `printf %q`
# porque este escapa los bytes de las letras con tilde según la configuración
# regional y dejaría el texto ilegible.
shquote() {
  local s="${1//\'/\'\\\'\'}"
  printf "'%s'" "$s"
}

# ----------------------------------------------------------------- escenario

run_scenario() {
  local s="$1"
  local sdir="$OUT/$s" work="$OUT/$s/work"
  local home="$work/home" repo="$work/repo"

  rm -rf "$sdir"
  mkdir -p "$home" "$repo"
  list_real_mneme >"$work/real_before.txt"

  isolate_env "$home"
  git config --global user.name "skill-comply"
  git config --global user.email "skill-comply@example.com"

  mneme install claude-code </dev/null >"$work/install.log" 2>&1 || die_harness "mneme install claude-code falló (ver $work/install.log)"

  cp -R "$SCRIPT_DIR/fixture/." "$repo/"
  cd "$repo"
  git init -q -b main
  # El remote fija el nombre del proyecto en mneme (skill-comply/fixture).
  git remote add origin https://github.com/skill-comply/fixture.git
  mneme init </dev/null >"$work/init.log" 2>&1 || die_harness "mneme init falló (ver $work/init.log)"
  git add .
  git commit -q -m "base: fixture de skill-comply"
  git rev-parse HEAD >"$work/base_sha"

  if ! compgen -G "$home/.mneme/projects/*.db" >/dev/null; then
    die_harness "el HOME aislado no se usó: no hay ninguna base .db en $home/.mneme/projects"
  fi

  local prompt
  prompt="$(cat "$SCRIPT_DIR/scenarios/$s/prompt.md")"
  if [[ "$s" == "present-spec" ]]; then
    WORK="$work" bash "$SCRIPT_DIR/scenarios/present-spec/seed.sh" || die_harness "la siembra de present-spec falló"
    prompt="${prompt//\{\{SPEC_ID\}\}/$(cat "$work/spec_id")}"
  fi

  local cmd=("$CLAUDE" -p "$prompt" --output-format stream-json --verbose
    --permission-mode acceptEdits --max-turns "$MAX_TURNS")
  [[ -z "$MODEL" ]] || cmd+=(--model "$MODEL")
  cmd+=(--allowedTools "$ALLOWED_TOOLS")
  : >"$sdir/command.txt"
  local arg
  for arg in "${cmd[@]}"; do
    printf '%s ' "$(shquote "$arg")" >>"$sdir/command.txt"
  done
  printf '\n' >>"$sdir/command.txt"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "[$s] --dry-run: orden preparada en $sdir/command.txt"
    cat "$sdir/command.txt"
  else
    run_claude "$s" "$sdir" "$work" "${cmd[@]}"
  fi

  "$CHECK" snapshot --mneme "$BIN_DIR/mneme" --home "$home" --repo "$repo" --out "$sdir/state.json" \
    || die_harness "check snapshot falló en $s"

  assert_real_untouched "$work/real_before.txt"

  local rc=0
  if [[ "$DRY_RUN" -eq 0 ]]; then
    local extra=()
    if [[ "$s" == "present-spec" ]]; then
      extra=(--markers "$SCRIPT_DIR/scenarios/present-spec/markers.txt"
        --criteria "$SCRIPT_DIR/scenarios/present-spec/criteria.toml")
    fi
    "$CHECK" verify --scenario "$s" --transcript "$sdir/transcript.jsonl" \
      --state "$sdir/state.json" ${extra[@]+"${extra[@]}"} >"$sdir/verify.txt" || rc=$?
    cat "$sdir/verify.txt"
  fi

  if [[ "$KEEP" -eq 0 ]]; then
    rm -rf "${work:?}/home" "${work:?}/repo"
  fi
  return "$rc"
}

# run_claude lanza claude con vigilante de tiempo (macOS no trae `timeout`).
run_claude() {
  local s="$1" sdir="$2" work="$3"
  shift 3

  if [[ "$PREFLIGHT" -eq 1 && ! -e "$OUT/.preflight_ok" ]]; then
    local pre
    pre="$(cd "$work" && "$CLAUDE" -p "Responde solo: OK" --max-turns 1 --output-format json </dev/null 2>"$work/preflight.err" || true)"
    if [[ "$pre" != *'"result"'* ]]; then
      die_harness "claude no pudo autenticarse en el HOME aislado; exporta CLAUDE_CODE_OAUTH_TOKEN (claude setup-token) o ANTHROPIC_API_KEY."
    fi
    : >"$OUT/.preflight_ok"
  fi

  local timed_out="$work/timed_out"
  rm -f "$timed_out"
  echo "[$s] lanzando claude (límite ${TIMEOUT}s, ${MAX_TURNS} turnos)..."
  "$@" >"$sdir/transcript.jsonl" 2>"$sdir/claude.stderr" </dev/null &
  local pid=$!
  (
    sleep "$TIMEOUT" &
    sleeper=$!
    # Si el escenario termina antes, el padre manda TERM y el vigilante se va
    # sin dejar su `sleep` huérfano.
    trap 'kill "$sleeper" 2>/dev/null; exit 0' TERM
    wait "$sleeper"
    : >"$timed_out"
    kill -TERM "$pid" 2>/dev/null || true
    sleep 5
    kill -KILL "$pid" 2>/dev/null || true
  ) >/dev/null 2>&1 &
  local watcher=$!
  wait "$pid" || true
  kill -TERM "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true

  if [[ -e "$timed_out" ]]; then
    die_harness "tiempo agotado en $s (${TIMEOUT}s); ver $sdir/claude.stderr"
  fi
}

# ------------------------------------------------------------------ principal

overall=0
for s in "${SCENARIOS[@]}"; do
  echo "== escenario: $s =="
  rc=0
  # `set -e` se desactiva en el padre durante la llamada; el subshell lo reactiva.
  set +e
  (
    set -euo pipefail
    run_scenario "$s"
  )
  rc=$?
  set -e
  if [[ "$rc" -eq 2 ]]; then
    echo "skill-comply: fallo del arnés en $s; aborto la corrida" >&2
    exit 2
  fi
  if [[ "$rc" -ne 0 && "$overall" -eq 0 ]]; then
    overall=1
  fi
done

if [[ "$DRY_RUN" -eq 0 ]]; then
  "$CHECK" summary --out "$OUT" || die_harness "check summary falló"
fi
echo "salidas en: $OUT"
exit "$overall"
