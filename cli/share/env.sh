# ezc-shim: 2
#
# Loads this repo's environment from ezc. Written by `ezc repo init`; do not edit.
#
#   mise:    [env]  _.source = ".ezc/env.sh"
#   direnv:  source_env .ezc/env.sh
#
# It never fails the shell: a broken config must not make `cd` unusable.
# `ezc env` is the check that fails, so make tasks depend on it.
#
# EZC_SHIM_LOADING marks the ezc this runs: an ezc that mise installed is a
# mise shim, which loads this file before it runs ezc, and without the mark
# that would run ezc again, without end.
if [ -z "${EZC_SHIM_LOADING:-}" ] && command -v ezc >/dev/null 2>&1; then
  eval "$(EZC_SHIM_LOADING=1 ezc env export 2>/dev/null || true)"
fi
