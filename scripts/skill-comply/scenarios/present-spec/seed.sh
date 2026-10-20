#!/usr/bin/env bash
# Siembra el escenario present-spec: deja una spec lane standard en `specced`
# con su spec.md y criteria.toml, usando solo la CLI de mneme del HOME aislado.
#
# Entorno requerido (lo prepara run.sh): HOME aislado, `mneme` en el PATH y
# WORK (directorio de trabajo del escenario, donde se escribe spec_id).
# Se ejecuta con el directorio actual dentro del repositorio de prueba.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${WORK:?seed.sh: falta WORK}"

created="$(mneme backlog add "Función Reverse en textutil" --lane standard \
  --description "Agregar Reverse(s string) string a textutil.")"
bl_id="$(printf '%s\n' "$created" | sed -n 's/^Created \(BL-[0-9]*\).*/\1/p' | head -n 1)"
[[ -n "$bl_id" ]] || { echo "seed.sh: no pude leer el id del ítem de backlog" >&2; exit 2; }

mneme backlog refine "$bl_id" --refinement "Firma fija, cadena vacía devuelve vacía, prueba table-driven." >/dev/null
promoted="$(mneme backlog promote "$bl_id")"
spec_id="$(printf '%s\n' "$promoted" | sed -n 's/.*-> \(SPEC-[0-9]*\).*/\1/p' | head -n 1)"
[[ -n "$spec_id" ]] || { echo "seed.sh: no pude leer el id de la spec" >&2; exit 2; }

mneme spec advance "$spec_id" --by seed >/dev/null # draft -> speccing

spec_dir="$(find "$HOME/.mneme/workflows" -type d -path "*/specs/$spec_id" | head -n 1)"
[[ -n "$spec_dir" ]] || { echo "seed.sh: no encuentro el directorio de $spec_id" >&2; exit 2; }
cp "$here/spec.md" "$spec_dir/spec.md"
cp "$here/criteria.toml" "$spec_dir/criteria.toml"

mneme spec advance "$spec_id" --by seed >/dev/null # speccing -> specced

printf '%s\n' "$spec_id" >"$WORK/spec_id"
