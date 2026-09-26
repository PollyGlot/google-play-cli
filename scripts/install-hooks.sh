#!/usr/bin/env bash
# install-hooks.sh: opt-in installer for the versioned hooks in .githooks/.
#
# It writes a small shim into the hooks directory git actually reads, and the
# shim delegates to .githooks/<hook> of whichever worktree runs it. Two choices
# follow from how this repo is used:
#
# - `git rev-parse --git-path hooks` rather than `.git/hooks`: in a worktree
#   `.git` is a file, and the hooks directory is shared through the common dir
#   (or wherever core.hooksPath points). One install covers every worktree.
# - A shim rather than `git config core.hooksPath .githooks`: repointing
#   core.hooksPath would silently disable hooks already installed there (a
#   post-checkout hook, for example), and a delegating shim never goes stale
#   when .githooks/ changes. A branch that predates .githooks/ simply skips.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

hooks_dir="$(git rev-parse --path-format=absolute --git-path hooks)"
marker="# gplay-hook-shim"
mkdir -p "$hooks_dir"

for src in .githooks/*; do
	name="$(basename "$src")"
	dest="$hooks_dir/$name"
	if [ -e "$dest" ] && ! grep -qF "$marker" "$dest"; then
		echo "install-hooks: $dest already exists and is not ours; left untouched." >&2
		echo "install-hooks: call .githooks/$name from it to opt in." >&2
		exit 1
	fi
	cat >"$dest" <<SHIM
#!/bin/sh
$marker: installed by \`make install-hooks\`, delegates to the versioned hook.
hook="\$(git rev-parse --show-toplevel)/.githooks/$name"
[ -x "\$hook" ] || exit 0
exec "\$hook" "\$@"
SHIM
	chmod +x "$dest"
	echo "install-hooks: $name -> $dest"
done
