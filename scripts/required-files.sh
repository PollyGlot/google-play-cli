#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

missing=0
for f in \
	CLAUDE.md \
	GLOSSARY.md \
	CONTRIBUTING.md \
	CODE_OF_CONDUCT.md \
	LICENSE \
	README.md \
	SUPPORT.md \
	docs/DESIGN.md \
	docs/CI_CD.md \
	docs/adr/0001-credential-storage.md \
	docs/adr/0002-safe-production-defaults.md \
	docs/adr/0003-json-passthrough.md; do
	if [ ! -f "$f" ]; then
		echo "::error file=$f::missing required file"
		missing=$((missing + 1))
	fi
done
exit "$missing"
