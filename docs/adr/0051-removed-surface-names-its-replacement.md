# A removed verb or flag names its replacement

## Status

accepted

## Context

gplay removes surface without aliases: the verb vocabulary of
[ADR-0019](./0019-canonical-verb-vocabulary.md) dropped `apps info`,
`tracks status` and `team grants revoke` in 0.4.1, and the 2.0.0 vocabulary
pass ([ADR-0048](./0048-v2-release-train.md), #597) renamed a dozen verbs and
flags. That break is deliberate: a stale script stops on its first run instead
of doing something else.

What the break left out is the way back. A 1.x CI job, or an agent trained on
the old surface, got `unknown command "update" for "gplay games achievements"`,
exit 2, and nothing else. The only hint the kernel gave was cobra's Levenshtein
suggestion, silent on most renames and sometimes wrong (`gplay apps info`
suggested `init`, not `view`). Worse, `gplay games achievements update <id>
--output json` failed on `unknown flag: --output`: cobra resolves the command
path first, `update` is no longer a child, so the flags are parsed on the group,
which has no `--output`. The message did not even name the removed verb. The
information to recover lived only in the website's migration guide.

## Decision

1. **One declarative table of removed surface**, in `internal/kernel/removed.go`,
   owned by the kernel rather than by each command. A row holds the kind
   (`command` or `flag`), the command path the removed token applied to, the
   token (a verb, several words when a branch moved, or a flag name), the
   replacement, the version of removal and an optional one-line note for a
   change that is not a pure rename (`appstore update` became the domain verb
   `appstore submit`).
2. **Two lookup points.** `kernel.GroupRunE`, which already raises every unknown
   command (the root included), and `kernel.FlagErrorFunc`, the root's
   flag-error func: when a flag fails to parse on a **group** whose leading
   positional arguments are a removed verb, the removed-verb error wins over the
   flag error. The flag may sit before the verb (`gplay games achievements
   --output json update <id>`): pflag stops on it and never reads the verb, so
   the func reads the positionals of the raw argv (handed over by
   `kernel.SetArgs`) that follow the group's path, skipping flags the way
   cobra's own path resolution does: a valued flag, known or unknown, consumes
   the next token, so a flag value is never taken for a verb. The remaining
   limit is cobra's: an unknown flag that takes no value, typed before the
   removed verb (`--bogus update`), swallows the verb, and the call keeps
   pflag's `unknown flag` message. When an unknown long flag fails on a
   **leaf**, the func looks up
   a `flag` row for that exact leaf. pflag names the flag the same way for
   `--to 0.1` and `--to=0.1`; no removed flag had a shorthand, so a shorthand
   error never matches.
3. **Exact match only.** The table answers "is this a path gplay removed", never
   "what did you probably mean". A typo of a live verb (`apps veiw`) keeps the
   existing suggestion, and so does a misspelled removed verb (`updat`).
4. **The table beats did-you-mean.** On a match the replacement replaces the
   Levenshtein block: it is authoritative, the guess is not.
5. **Same error, more information.** Still an `exit.UsageError`: exit 2,
   `USAGE_ERROR`, the same first words (`unknown command "update" for "gplay
   games achievements"`). The message gains ``: removed in <version>, use
   `gplay <replacement>` `` and the note in parentheses. The error carries a
   typed `exit.Replacement`, which the JSON error envelope reads with
   `errors.As` into an additive, omitempty `replacement` object (`kind`,
   `removed`, `use`, `removedIn`), the way `requires` is read from the
   safety-flag error. The envelope stays append-only
   ([ADR-0023](./0023-json-error-envelope.md),
   [ADR-0044](./0044-structured-diagnostic-codes.md)).

   ```json
   {
     "error": {
       "code": "USAGE_ERROR",
       "exitCode": 2,
       "retryable": false,
       "message": "unknown command \"update\" for \"gplay games achievements\": removed in 2.0.0, use `gplay games achievements set`",
       "replacement": {
         "kind": "command",
         "removed": "gplay games achievements update",
         "use": "gplay games achievements set",
         "removedIn": "2.0.0"
       }
     }
   }
   ```

   A removed flag reads the same way, `use` naming the full command in the
   message and the bare flag in the envelope, since the command is the one that
   failed:

   ```json
   {
     "error": {
       "code": "USAGE_ERROR",
       "exitCode": 2,
       "retryable": false,
       "message": "unknown flag: --to: removed in 2.0.0, use `gplay releases rollout --staged`",
       "replacement": {
         "kind": "flag",
         "removed": "--to",
         "use": "--staged",
         "removedIn": "2.0.0"
       }
     }
   }
   ```
6. **The table cannot rot.** `TestRemovedSurface_table` (`cmd/gplay`) walks the
   live command tree: every replacement resolves to a runnable leaf (a flag of
   that leaf, for a flag row), every removed path or flag resolves to nothing,
   every group or leaf a row hangs off still exists. A row of a kind the test
   does not check fails it.
7. **A rename ships its row.** Removing or renaming a verb or flag adds the row
   in the same pull request. The migration guide stays the human mirror; the
   table is the source the binary reads.

## Consequences

- An agent recovers from a removed verb by reading `error.replacement.use`, with
  no human and no prose parsing; a CI log names the fix in one line.
- Exit codes, diagnostic codes and the first line of the message are unchanged,
  so existing error handling keeps working.
- Bare paths that became groups (`apps details`, `tracks availability`) print
  their group help and exit 0; they are not errors and stay out of the table.
- `gplay <group> <removed-verb> --help` still prints the group help (exit 0)
  without a hint.
- Removed flags take `flag` rows in the same table, one per leaf that lost the
  flag (the vitals `--version` rename is eight rows), so a flag removed from
  one command (`releases rollout --to`) is never reported on another that keeps
  it (`releases promote --to`).

## Alternatives rejected

1. **cobra aliases.** Rejected: an alias runs the old surface, which undoes the
   2.0.0 contract and keeps two names per concept alive indefinitely.
2. **Hidden deprecated commands** that only print the hint. Rejected: one stub
   command per removed verb scatters the inventory across the tree, each stub
   needs its own flags to avoid the group-flag trap, and a hidden command still
   resolves through cobra, so the integrity check of decision 6 could not tell
   a removed path from a live one.
3. **Fuzzy matching against the table.** Rejected: a guess presented as
   authoritative is worse than the honest Levenshtein suggestion it would
   replace.
4. **Only the migration guide.** Rejected: that is the situation this ADR
   fixes; the information has to be where the error happens.
