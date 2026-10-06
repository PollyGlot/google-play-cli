// Package workflowgate holds offline checks over the repository's GitHub
// Actions workflows that actionlint does not make. It has no code of its own:
// the checks are tests, so they run in `make check` and in the required CI job.
package workflowgate
