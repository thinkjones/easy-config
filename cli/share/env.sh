# ezc-shim: 1
#
# Loads this repo's environment from ezc. Written by `ezc repo init`; do not edit.
#
#   mise:    [env]  _.source = ".ezc/env.sh"
#   direnv:  source_env .ezc/env.sh
#
# It never fails the shell: a broken config must not make `cd` unusable.
# `ezc env` is the check that fails, so make tasks depend on it.
if command -v ezc >/dev/null 2>&1; then
  eval "$(ezc env export 2>/dev/null || true)"
fi
