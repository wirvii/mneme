#!/usr/bin/env bash
# enforce_delegation.sh — PreToolUse hook
#
# Bloquea que el orquestador (sesión principal de Claude Code) modifique
# archivos fuera de una whitelist mínima. Los subagentes (lanzados via
# Task/Agent tool) NO son afectados — se detectan por la presencia del
# campo `agent_id` en el JSON de stdin.
#
# Whitelist (lo que el orquestador SÍ puede tocar):
#   - .claude/**         (config local del proyecto)
#   - ~/.claude/**       (config global)
#   - CLAUDE.md          (en cualquier ubicación)
#   - **/docs/*.md       (documentación)
#   - .claudeignore      (en la raíz)
#
# Cobertura:
#   - Write, Edit, MultiEdit, NotebookEdit (file_path directo)
#   - Bash (redirects >, >>, 2>; sed -i; mv/cp/rm/touch/chmod/chown/ln/dd/
#           patch/install/truncate/tee; here-docs; python -c / node -e con escritura)
#
# Salida:
#   - exit 0: permitido (subagente, herramienta no relevante, o path en whitelist)
#   - exit 2: bloqueado (Claude Code rechaza la herramienta y muestra stderr al agente)
#
# Fail-open: cualquier error de parsing o jq → exit 0. Mejor no bloquear
# falsamente que bloquear y romper el flujo.

set -u

# ---------------------------------------------------------------------------
# 1. Leer stdin
# ---------------------------------------------------------------------------
INPUT="$(cat)"
if [[ -z "$INPUT" ]]; then
  exit 0
fi

# ---------------------------------------------------------------------------
# 2. Skip si es subagente (campo agent_id presente en el JSON)
# ---------------------------------------------------------------------------
AGENT_ID="$(printf '%s' "$INPUT" | jq -r '.agent_id // empty' 2>/dev/null)"
if [[ -n "$AGENT_ID" ]]; then
  exit 0
fi

# ---------------------------------------------------------------------------
# 3. Solo interceptamos herramientas que modifican archivos
# ---------------------------------------------------------------------------
TOOL_NAME="$(printf '%s' "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null)"
case "$TOOL_NAME" in
  Write|Edit|MultiEdit|NotebookEdit|Bash) ;;
  *) exit 0 ;;
esac

# ---------------------------------------------------------------------------
# 4. Whitelist — un path es permitido si:
#    - es .claudeignore (basename exacto)
#    - es CLAUDE.md (basename exacto)
#    - termina en .md y está bajo /docs/
#    - contiene /.claude/ (absoluto) o empieza con .claude/ (relativo)
# ---------------------------------------------------------------------------
is_allowed_path() {
  local path="$1"
  # Strip comillas y ./ prefix
  path="${path#./}"
  path="${path//\'/}"
  path="${path//\"/}"

  local basename="${path##*/}"

  [[ "$basename" == "CLAUDE.md" ]] && return 0
  [[ "$basename" == ".claudeignore" ]] && return 0

  # *.md bajo cualquier /docs/
  if [[ "$basename" == *.md && "/${path}" == */docs/* ]]; then
    return 0
  fi

  # Path absoluto: debe contener /.claude/
  if [[ "$path" == /* ]]; then
    [[ "$path" == */.claude/* ]] && return 0
    return 1
  fi

  # Path relativo: debe empezar con .claude/
  [[ "$path" == .claude/* || "$path" == ".claude" ]] && return 0

  return 1
}

# ---------------------------------------------------------------------------
# 5. Imprimir bloqueo y exit 2
# ---------------------------------------------------------------------------
block() {
  local reason="$1"
  printf 'BLOQUEADO: El orquestador NO puede modificar archivos fuera de la whitelist.\n' >&2
  printf 'Razón: %s\n' "$reason" >&2
  printf 'Whitelist: .claude/**, ~/.claude/**, CLAUDE.md, **/docs/*.md, .claudeignore\n' >&2
  printf 'ACCIÓN: Delegá al subagente correspondiente (Agent tool con subagent_type=backend|frontend|architect|...).\n' >&2
  printf 'Tu trabajo es coordinar y conversar, NO implementar código.\n' >&2
  exit 2
}

# ---------------------------------------------------------------------------
# 6. Checker para herramientas de archivo (file_path en tool_input)
# ---------------------------------------------------------------------------
check_file_tool() {
  local file_path
  file_path="$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // .tool_input.notebook_path // .tool_input.path // empty' 2>/dev/null)"

  # Sin file_path → permitir (no es nuestro caso)
  [[ -z "$file_path" ]] && exit 0

  if is_allowed_path "$file_path"; then
    exit 0
  fi

  block "Ruta bloqueada: '$file_path'"
}

# ---------------------------------------------------------------------------
# 7. Checker para Bash — detectar escrituras encubiertas
# ---------------------------------------------------------------------------
check_bash() {
  local command
  command="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)"
  [[ -z "$command" ]] && exit 0

  # --- 7.1 Redirects: >, >>, 2>, &>, >| (excepto /dev/*)
  # Match: [012&]?>>? optional |, espacios, captura del target
  local rest="$command"
  while [[ "$rest" =~ ([012\&]?\>\>?\|?)[[:space:]]*([^[:space:]\;\|\&\<]+) ]]; do
    local target="${BASH_REMATCH[2]}"
    target="${target//\'/}"
    target="${target//\"/}"
    if [[ "$target" != /dev/* ]]; then
      if ! is_allowed_path "$target"; then
        block "Redirect a ruta protegida: '$target'"
      fi
    fi
    # avanzar el cursor para el próximo match
    rest="${rest#*"${BASH_REMATCH[0]}"}"
  done

  # Helper: boundary izquierda = inicio, espacio, ;, |, & o (
  local BL='(^|[[:space:]\;\|\&\(])'

  # --- 7.2 sed -i / perl -i fuera de .claude/
  if [[ "$command" =~ ${BL}sed[[:space:]]+(-[a-zA-Z]*i|--in-place) ]]; then
    if [[ "$command" != *".claude/"* && "$command" != *"CLAUDE.md"* ]]; then
      block "sed -i fuera de .claude/"
    fi
  fi
  if [[ "$command" =~ ${BL}perl[[:space:]]+-[a-zA-Z]*i ]]; then
    if [[ "$command" != *".claude/"* && "$command" != *"CLAUDE.md"* ]]; then
      block "perl -i fuera de .claude/"
    fi
  fi

  # --- 7.3 Comandos de manipulación de archivos
  # Helper: para cada comando, extraer el último arg antes de ; | & o fin
  # y validarlo contra la whitelist
  last_arg_after_cmd() {
    local cmd_name="$1"
    # Aislar el segmento desde el comando hasta el siguiente separador.
    # Ignora redirects (>, >>, 2>, &>, 2>&1, >file, etc.) y sus targets.
    printf '%s' "$command" | awk -v c="$cmd_name" '
      {
        n = split($0, tokens, /[[:space:]]+/)
        for (i = 1; i <= n; i++) {
          if (tokens[i] == c) {
            last = ""
            for (j = i + 1; j <= n; j++) {
              t = tokens[j]
              if (t == ";" || t == "|" || t == "||" || t == "&" || t == "&&") break
              # Skip redirects: 2>/dev/null, >file, >>file, &>file, 2>&1, etc.
              if (t ~ /^[0-9]*[&]?[<>]+/) {
                # Si el operador es standalone (>, >>, 2>), brincar tambien el target
                if (t ~ /^[0-9]*[&]?[<>]+$/) { j++ }
                continue
              }
              last = t
            }
            print last
            exit
          }
        }
      }'
  }

  for cmd_name in tee mv cp rm rmdir touch chmod chown ln install patch truncate; do
    if [[ "$command" =~ ${BL}${cmd_name}[[:space:]] ]]; then
      local target
      target="$(last_arg_after_cmd "$cmd_name")"
      target="${target//\'/}"
      target="${target//\"/}"
      # Filtrar flags que pudieran quedar como último arg
      if [[ -n "$target" && "$target" != -* ]] && ! is_allowed_path "$target"; then
        block "'$cmd_name' a ruta protegida: '$target'"
      fi
    fi
  done

  # dd of=...
  if [[ "$command" =~ ${BL}dd[[:space:]].*of=([^[:space:]\;\|\&]+) ]]; then
    local target="${BASH_REMATCH[2]}"
    target="${target//\'/}"
    target="${target//\"/}"
    if ! is_allowed_path "$target"; then
      block "'dd' a ruta protegida: '$target'"
    fi
  fi

  # --- 7.4 Here-doc con redirect: <<EOF ... > target
  if [[ "$command" =~ \<\<-?[[:space:]]*[\'\"]?[A-Za-z_][A-Za-z0-9_]*[\'\"]?.*\>[[:space:]]*([^[:space:]\;\|\&]+) ]]; then
    local target="${BASH_REMATCH[1]}"
    target="${target//\'/}"
    target="${target//\"/}"
    if ! is_allowed_path "$target"; then
      block "Heredoc a ruta protegida: '$target'"
    fi
  fi

  # --- 7.5 Scripts inline que escriben archivos
  if [[ "$command" =~ ${BL}python[23]?[[:space:]]+-c[[:space:]] ]]; then
    if [[ "$command" =~ (open|write|Path) && "$command" != *".claude/"* ]]; then
      block "Script Python inline con escritura fuera de .claude/"
    fi
  fi
  if [[ "$command" =~ ${BL}node[[:space:]]+-e[[:space:]] ]]; then
    if [[ "$command" =~ (writeFile|appendFile|fs\.) && "$command" != *".claude/"* ]]; then
      block "Script Node inline con escritura fuera de .claude/"
    fi
  fi

  exit 0
}

# ---------------------------------------------------------------------------
# 8. Dispatch
# ---------------------------------------------------------------------------
case "$TOOL_NAME" in
  Write|Edit|MultiEdit|NotebookEdit) check_file_tool ;;
  Bash) check_bash ;;
esac

exit 0
