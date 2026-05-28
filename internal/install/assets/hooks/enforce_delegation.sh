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
# 0. Detectar si mneme hook tokenize está disponible (una sola vez al inicio)
#    Resultado: USE_GO_TOKENIZER=1 (robusto) o 0 (fallback al parser awk legacy)
# ---------------------------------------------------------------------------
USE_GO_TOKENIZER=0
if command -v mneme >/dev/null 2>&1 && printf '' | mneme hook tokenize >/dev/null 2>&1; then
  USE_GO_TOKENIZER=1
fi
if [[ "$USE_GO_TOKENIZER" -eq 0 ]]; then
  printf '[enforce_delegation] WARNING: mneme hook tokenize not available, using legacy parser (may produce false positives)\n' >&2
fi

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
# 7. Checker para Bash — versión robusta usando mneme hook tokenize
# ---------------------------------------------------------------------------

# BASH_C_DEPTH tracks recursive depth for "bash/sh/zsh -c <cmd>" detection.
# D5 (SPEC-033): recurse at most 1 level into the inner command string.
BASH_C_DEPTH=0

# check_bash_go: tokeniza el comando con 'mneme hook tokenize' y aplica las
# mismas reglas de matching sobre los tokens estructurados.
#
# Reglas de matching:
#   - type=redirect_target → validar path contra whitelist (excepto /dev/*)
#   - type=word && !quoted && value in comandos_vigilados → buscar target
#   - type=word && !quoted && value in (bash|sh|zsh) && next==-c → recurse 1 nivel
#   - type=command_substitution → re-tokenize 1 nivel ($() recursion)
#   - type=heredoc_body → skip (no es un comando)
#
# Comandos vigilados: tee mv cp rm rmdir touch chmod chown ln install patch truncate
#
# Retorna 0 si el comando es seguro, llama a block() si no lo es.
check_bash_go() {
  local command="$1"
  [[ -z "$command" ]] && return 0

  # Tokenizar el comando
  local tokens_json
  tokens_json="$(printf '%s' "$command" | mneme hook tokenize 2>/dev/null)"
  if [[ -z "$tokens_json" ]]; then
    return 0  # fail-open: tokenizador no disponible
  fi

  local n_tokens
  n_tokens="$(printf '%s' "$tokens_json" | jq '.tokens | length' 2>/dev/null)"
  if [[ -z "$n_tokens" || "$n_tokens" -eq 0 ]]; then
    return 0
  fi

  # Iterar tokens con jq para extraer (index, value, type, quoted)
  local i=0
  while IFS=$'\t' read -r tok_value tok_type tok_quoted; do
    case "$tok_type" in
      redirect_target)
        # Validar redirect targets (excepto /dev/*)
        if [[ "$tok_value" != /dev/* ]]; then
          if ! is_allowed_path "$tok_value"; then
            block "Redirect a ruta protegida: '$tok_value'"
          fi
        fi
        ;;
      word)
        if [[ "$tok_quoted" == "false" ]]; then
          # Detectar bash/sh/zsh -c <cmd> y recursar 1 nivel (D5, SPEC-033).
          case "$tok_value" in
            bash|sh|zsh)
              local next_val next_cmd
              next_val="$(printf '%s' "$tokens_json" | jq -r ".tokens[$((i+1))].value // empty" 2>/dev/null)"
              if [[ "$next_val" == "-c" ]]; then
                next_cmd="$(printf '%s' "$tokens_json" | jq -r ".tokens[$((i+2))].value // empty" 2>/dev/null)"
                if [[ -n "$next_cmd" && "$BASH_C_DEPTH" -lt 1 ]]; then
                  BASH_C_DEPTH=$((BASH_C_DEPTH+1))
                  check_bash_go "$next_cmd"
                  BASH_C_DEPTH=$((BASH_C_DEPTH-1))
                fi
              fi
              ;;
          esac
          # Verificar si es un comando vigilado como primer token del segmento
          case "$tok_value" in
            tee|mv|cp|rm|rmdir|touch|chmod|chown|ln|install|patch|truncate)
              # Buscar el último token word no-quoted antes de redirect/end
              local target
              target="$(_find_last_word_target "$tokens_json" "$i")"
              if [[ -n "$target" && "$target" != -* ]]; then
                if ! is_allowed_path "$target"; then
                  block "'$tok_value' a ruta protegida: '$target'"
                fi
              fi
              ;;
            sed)
              # sed -i es capturado aquí si el primer token es sed
              # verificar si hay -i en los siguientes tokens
              local next_tok
              next_tok="$(printf '%s' "$tokens_json" | jq -r ".tokens[$((i+1))].value // empty" 2>/dev/null)"
              if [[ "$next_tok" == -* && "$next_tok" == *i* ]]; then
                local cmd_str="$command"
                if [[ "$cmd_str" != *".claude/"* && "$cmd_str" != *"CLAUDE.md"* ]]; then
                  block "sed -i fuera de .claude/"
                fi
              fi
              ;;
            perl)
              local next_tok
              next_tok="$(printf '%s' "$tokens_json" | jq -r ".tokens[$((i+1))].value // empty" 2>/dev/null)"
              if [[ "$next_tok" == -* && "$next_tok" == *i* ]]; then
                local cmd_str="$command"
                if [[ "$cmd_str" != *".claude/"* && "$cmd_str" != *"CLAUDE.md"* ]]; then
                  block "perl -i fuera de .claude/"
                fi
              fi
              ;;
            dd)
              # dd of=target
              local j=$((i+1))
              local dd_target
              dd_target="$(printf '%s' "$tokens_json" | jq -r "
                .tokens[$j:] |
                map(select(.type == \"word\" and (.quoted // false) == false)) |
                map(select(.value | startswith(\"of=\"))) |
                first | .value // empty" 2>/dev/null)"
              if [[ -n "$dd_target" ]]; then
                dd_target="${dd_target#of=}"
                dd_target="${dd_target//\'/}"
                dd_target="${dd_target//\"/}"
                if ! is_allowed_path "$dd_target"; then
                  block "'dd' a ruta protegida: '$dd_target'"
                fi
              fi
              ;;
            python|python2|python3)
              # python -c con open/write/Path
              local full_cmd="$command"
              if [[ "$full_cmd" =~ (open|write|Path) && "$full_cmd" != *".claude/"* ]]; then
                block "Script Python inline con escritura fuera de .claude/"
              fi
              ;;
            node)
              # node -e con writeFile/appendFile/fs.
              local full_cmd="$command"
              if [[ "$full_cmd" =~ (writeFile|appendFile|fs\.) && "$full_cmd" != *".claude/"* ]]; then
                block "Script Node inline con escritura fuera de .claude/"
              fi
              ;;
          esac
        fi
        ;;
      command_substitution)
        # Re-tokenize 1 nivel (D5: recursion en bash, max 1 nivel)
        if [[ -n "$tok_value" ]]; then
          check_bash_go "$tok_value"
        fi
        ;;
      heredoc_body)
        # Skip: el contenido del heredoc NO se parsea como comandos (D4)
        ;;
    esac
    i=$((i+1))
  done < <(printf '%s' "$tokens_json" | jq -r '.tokens[] | [.value, .type, (.quoted // false | tostring)] | @tsv' 2>/dev/null)
}

# _find_last_word_target: dado el índice del token de comando vigilado, busca
# el último token de tipo "word" no-quoted antes del próximo redirect o fin.
_find_last_word_target() {
  local tokens_json="$1"
  local cmd_idx="$2"
  printf '%s' "$tokens_json" | jq -r "
    .tokens[($cmd_idx+1):] |
    # detener en el primer redirect (el segmento del comando termina ahí)
    . as \$arr |
    ([ \$arr[] | .type == \"redirect\" ] | index(true)) as \$redir_idx |
    (if \$redir_idx != null then \$arr[:(\$redir_idx)] else \$arr end) |
    # de ese segmento, tomar solo words no-quoted que no son flags
    map(select(.type == \"word\" and (.quoted // false) == false and (.value | startswith(\"-\") | not))) |
    last | .value // empty" 2>/dev/null
}

# ---------------------------------------------------------------------------
# 8. Checker para Bash — versión legacy usando awk (fallback)
# ---------------------------------------------------------------------------

# last_arg_after_cmd_legacy: helper del parser awk legacy.
# Extrae el último argumento no-redirect de un comando en el texto plano.
last_arg_after_cmd_legacy() {
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

# check_bash_legacy: parser original basado en awk/regex.
# Se usa cuando mneme hook tokenize no está disponible.
check_bash_legacy() {
  # --- 8.1 Redirects: >, >>, 2>, &>, >| (excepto /dev/*)
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

  # --- 8.2 sed -i / perl -i fuera de .claude/
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

  # --- 8.3 Comandos de manipulación de archivos
  for cmd_name in tee mv cp rm rmdir touch chmod chown ln install patch truncate; do
    if [[ "$command" =~ ${BL}${cmd_name}[[:space:]] ]]; then
      local target
      target="$(last_arg_after_cmd_legacy "$cmd_name")"
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

  # --- 8.4 Here-doc con redirect: <<EOF ... > target
  if [[ "$command" =~ \<\<-?[[:space:]]*[\'\"]?[A-Za-z_][A-Za-z0-9_]*[\'\"]?.*\>[[:space:]]*([^[:space:]\;\|\&]+) ]]; then
    local target="${BASH_REMATCH[1]}"
    target="${target//\'/}"
    target="${target//\"/}"
    if ! is_allowed_path "$target"; then
      block "Heredoc a ruta protegida: '$target'"
    fi
  fi

  # --- 8.5 Scripts inline que escriben archivos
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
}

# ---------------------------------------------------------------------------
# 9. Checker para Bash — dispatcher que elige Go o legacy
# ---------------------------------------------------------------------------
check_bash() {
  local command
  command="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)"
  [[ -z "$command" ]] && exit 0

  if [[ "$USE_GO_TOKENIZER" -eq 1 ]]; then
    check_bash_go "$command"
  else
    check_bash_legacy
  fi

  exit 0
}

# ---------------------------------------------------------------------------
# 10. Dispatch
# ---------------------------------------------------------------------------
case "$TOOL_NAME" in
  Write|Edit|MultiEdit|NotebookEdit) check_file_tool ;;
  Bash) check_bash ;;
esac

exit 0
