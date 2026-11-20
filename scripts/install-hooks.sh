#!/usr/bin/env bash
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
