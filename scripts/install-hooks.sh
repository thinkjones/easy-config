#!/bin/sh
# Install ezc's local gate into the hooks git will run.
#
# git runs the hooks in the directory core.hooksPath names, and .git/hooks when
# that is unset. Another tool may own that directory: beads installs its hooks
# into .beads/hooks and points core.hooksPath at it, so git never looks in
# .git/hooks at all and a hook file written there would never run. This script
# therefore never overwrites a hook file it did not write. It appends its block
# to a file another tool manages — outside that tool's own markers, which the
# tool preserves across its own installs — and writes the file itself when
# there is none.
#
# Run by mise when it installs this project's tools, and when a shell enters
# this project. It changes nothing it does not own, and it never fails the
# caller: the CI Gate is the enforcement, this is the fast path in front of it.

set -u

marker='ezc local gate'

warn() {
	echo "install-hooks: $*" >&2
}

# CI runs the same gate on the pull request; the hooks are not needed there.
[ "${CI:-}" = "true" ] && exit 0

root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0

hooks=$(git rev-parse --git-path hooks 2>/dev/null) || exit 0
case "$hooks" in
/*) ;;
*) hooks="$root/$hooks" ;;
esac

mkdir -p "$hooks" 2>/dev/null || {
	warn "cannot make $hooks"
	exit 0
}

# The block for one hook. The gate itself is lefthook's: the same config and
# the same commands, run by `lefthook run <hook>`.
block() {
	hook=$1
	echo "# --- $marker ---"
	if [ "$hook" = "pre-push" ]; then
		cat <<'EOF'
# Git hands a pre-push hook the refs it is about to push, one per line:
#   <local ref> <local sha> <remote ref> <remote sha>
ezc_pushed_refs=$(cat)
if printf '%s\n' "$ezc_pushed_refs" | awk '{print $3}' | grep -qx 'refs/heads/main'; then
  echo "Refusing to push to main." >&2
  echo "main is merged through a pull request and the CI Gate. Run the gate yourself first:" >&2
  echo "  mise run cli:test:sops" >&2
  echo "Bypass with: git push --no-verify." >&2
  exit 1
fi
EOF
		cat <<'EOF'
if command -v lefthook >/dev/null 2>&1; then
  printf '%s\n' "$ezc_pushed_refs" | lefthook run pre-push "$@" || exit $?
elif command -v mise >/dev/null 2>&1; then
  printf '%s\n' "$ezc_pushed_refs" | mise exec -- lefthook run pre-push "$@" || exit $?
else
  echo "The local gate could not run: neither lefthook nor mise is on PATH." >&2
  echo "CI runs the same gate on the pull request (mise run cli:test:sops)." >&2
  exit 1
fi
EOF
	else
		cat <<'EOF'
if command -v lefthook >/dev/null 2>&1; then
  lefthook run pre-commit "$@" || exit $?
elif command -v mise >/dev/null 2>&1; then
  mise exec -- lefthook run pre-commit "$@" || exit $?
else
  echo "The local gate could not run: neither lefthook nor mise is on PATH." >&2
  echo "CI runs the same gate on the pull request (mise run cli:test)." >&2
  exit 1
fi
EOF
	fi
	echo "# --- END $marker ---"
}

for hook in pre-commit pre-push; do
	file="$hooks/$hook"
	if [ -f "$file" ]; then
		# Already ours, or lefthook's own hook file (lefthook writes one on
		# its first run, and it does the same thing). Either way, leave it.
		if grep -q "$marker" "$file" 2>/dev/null || grep -q "call_lefthook" "$file" 2>/dev/null; then
			continue
		fi
		# Another tool's hook. The block goes at the end: that tool keeps the
		# content outside its own markers, so the block survives its installs.
		{
			echo ""
			block "$hook"
		} >>"$file" 2>/dev/null || warn "cannot write $file"
	else
		{
			echo "#!/bin/sh"
			block "$hook"
		} >"$file" 2>/dev/null || {
			warn "cannot write $file"
			continue
		}
		chmod +x "$file" 2>/dev/null || warn "cannot make $file executable"
	fi
done

exit 0
