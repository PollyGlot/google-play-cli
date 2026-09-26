#!/usr/bin/env bash
# required-files.sh: fails if a file the repo promises to contributors is gone.
#
# One list for two callers: the `Docs sanity` required check in ci.yml and
# `make check`. Keeping it here rather than inline in the workflow is what lets
# the local gate stay identical to CI without a copy that drifts.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

missing=0
for f in \
	CLAUDE.md \
	CONTEXT.md \
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
		# The ::error:: prefix annotates the file in the GitHub UI and reads
		# as plain text locally.
		echo "::error file=$f::missing required file"
		missing=$((missing + 1))
	fi
done
exit "$missing"
