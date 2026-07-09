#!/usr/bin/env bash
# mneme delegation hook (compat shim) — logic ported to Go (SPEC-069).
# This file only forwards to the mneme binary; do not edit.
exec mneme hook enforce-delegation
