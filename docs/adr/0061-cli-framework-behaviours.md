# ADR-0061: CLI framework behaviours gplay works around or relies on

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

gplay's command tree is built on cobra and its flag parser pflag. Several of
the CLI guarantees in DESIGN §9 (exit 2 for every kind of misuse, a removed
verb naming its replacement, no last-wins flag) hold only because the kernel
compensates for something these libraries do by default, or leans on
something they do without saying so. Each one shaped a piece of
`internal/kernel`. These decisions lived in code comments; the comment purge
(the `scripts/lint/comments.py` ratchet) gathers them here.

This ADR is a catalogue. Each entry states what the library does, what gplay
does about it, and why. A new entry goes under the mechanism it concerns.
Versions in force when it was written: cobra v1.10.2, pflag v1.1.0; a bump of
either is the moment to re-read it.

## Decision

### Unknown subcommands (`kernel.GroupRunE`)

- **A non-runnable command short-circuits to its help before validating its
  arguments.** A grouping noun with only an `Args` hook would print help and
  exit 0 on a mistyped or removed subcommand. Every group therefore gets a
  `RunE`, which prints help when bare and rejects a leftover token as misuse
  (exit 2).
- **cobra's default validator rejects unknown commands only on the root**
  (`legacyArgs`, raised from `Find` before `execute`, for a command with
  subcommands and no parent), and with an untyped error that maps to exit 1.
  The root sets `Args: cobra.ArbitraryArgs` beside `GroupRunE`, so it routes
  through the same exit-2 path as every group.
- **A group sets `SilenceUsage` and `SilenceErrors`**, so the failure is the
  single `gplay: ...` line main prints, without cobra's `Error:` and usage dump
  on top.
- **cobra applies `SuggestionsMinimumDistance`'s default (2) only in its own
  error path**, which `GroupRunE` bypasses, so gplay applies the same default
  itself. The matching stays cobra's (`SuggestionsFor`); only the rendering is
  gplay's.

### Positional arguments (`kernel.WrapArgErrors`)

- **cobra returns an `Args` validator's error untouched**, so it never
  reaches the flag-error func and carries no exit code. The wrapper re-types
  it as a usage error, once, over the assembled tree.
- **A nil `Args` is left alone.** A grouping noun keeps it nil so leftover
  tokens reach `GroupRunE`; a validator there would run first and break the
  bare-group help. Nothing is lost, since cobra treats a nil `Args` as
  `ArbitraryArgs`, and the one nil-`Args` path that can fail (the root's
  `legacyArgs`) is neutralised as above.
- **An error that already carries an exit code keeps it.** A validator that
  returns a safety-flag refusal (3) or a client-side validation error (20) is
  not downgraded; 2 is the fallback for cobra's untyped errors only. This also
  makes a second pass a no-op: the inner wrapper's error already carries a
  code, so the outer one passes it through.
- **`cobra.NoArgs` is recognised by comparing code pointers**, the one way to
  identify a top-level func value in Go, so its rejection can be reworded
  without matching cobra's message text.
- **Flags are parsed before cobra runs the `Args` validator**, so
  `Flags().Changed` is reliable inside one. `apps init` relies on it: with
  `--package` already given, a stray positional is not a misplaced package
  name, and the hint suggesting `--package` is left out.
- **cobra adds `help` and `completion` lazily.** The root materialises them
  before the walk so their validators are covered. The hidden `__complete`
  command is injected inside `Execute` with no earlier hook, and stays out of
  reach.

### Repeated flags (`kernel.RejectRepeatedFlags`)

- **The guard wraps each flag's value** and rejects the second `Set`. It
  embeds the real value, so `String`, `Type` and the pflag behaviours that read
  them (typed getters, a boolean's no-option default) keep working.
- **pflag decides whether to print `(default X)` by switching on its own
  concrete value types.** Any wrapper falls through to a generic branch that
  knows only `""`, `0`, `false` and `<nil>` as zero, so `--timeout` would start
  advertising `(default 0s)` on every command and in the generated website
  reference. The help and usage functions unwrap the flags while rendering and
  restore them after: parsing is over by then, and the restore keeps the swap
  invisible to a caller that renders help and carries on (a test, a library).
  cobra inherits these functions down the tree, so one install on the root
  covers every leaf, `gplay help <cmd>` and the usage block after an error.
- **pflag wraps a `Set` failure as `invalid argument "<second>" for
  "--flag" flag: <message>`**, so gplay's message names only the first value;
  repeating the flag and the second value would stutter.
- **Map values publish no interface.** Slice values implement
  `pflag.SliceValue`; the map values (`stringToString`, `stringToInt`,
  `stringToInt64`, still the full set in pflag v1.1.0) expose only
  `Set`/`String`/`Type`, so they are recognised by `Type()` name. A list of
  types, not of gplay flag names, keeps a new map flag working with no edit
  here, and the registry test in `cmd/gplay` reports any flag type it has
  never seen, so a fourth map kind cannot slip through.
- **A persistent flag is wrapped once, on the ancestor that declares it**,
  not again on each child that inherits it. An already-wrapped flag is
  skipped, which also makes the walk idempotent.
- **The repetition count lives in the wrapper**, and the tree is built fresh
  per process, so one invocation never sees another's count.
- **Completion requests parse their flags twice.** The guard is removed for
  the `__complete` path, which never reaches a `RunE` it could protect.

### Argv after a flag error (`kernel.FlagErrorFunc`, `kernel.SetArgs`)

- **cobra resolves the command path before parsing flags**, and **pflag stops
  at the first unknown flag**, which is why a removed verb is looked for in
  the raw argv ([ADR-0051](./0051-removed-surface-names-its-replacement.md)).
- **The argv reader imitates pflag's positional rules**: every token after
  `--` is positional, and so is a bare `-` (stdin, by convention).
- **A flag typed before the removed verb consumes the next token**, whether
  the group knows the flag or not, as cobra did when it resolved the path; the
  argv reader skips it the same way before looking for the verb.

### Flag values gplay validates itself

- **A value validated after pflag must raise exit 2 itself.** `--output` is a
  plain string flag whose value is checked downstream, not in `pflag.Set`, so
  the root's flag-error hook never sees a bad value: the format dispatcher
  returns an explicit `exit.UsageError`, one error for the unknown-format and
  missing-renderer paths alike, the same exit-2 path `--columns` uses for an
  unknown column.
- **A flag with a closed value set names it as its pflag type**, so `--help`
  renders `--if-exists skip|fail` rather than `string`. It must not report
  `string` in any case: the flag-type registry test in `cmd/gplay` feeds every
  `string` flag an arbitrary sample value, which a closed set rightly refuses.

### Tests

- **`Command.Find` returns no error on an unknown word.** It stops at the
  deepest command it reached and leaves the rest in its remaining arguments,
  so a test that a path resolves compares the found command's path, and a
  test that a name is gone checks that the name stayed unconsumed.
- **Parsing sets flag values on the tree it parses into.** A test that parses
  several command lines builds a fresh tree for each, so a value left by one
  can never satisfy a required flag of the next.
- **cobra adds `--help` to a command lazily, at execution.** A test that
  lists a command's flags skips it, or its output would depend on test order.
- **`os/exec`'s `Wait` closes the output pipes**, so a test reading a child's
  output drains the pipe before calling `Wait`, or it loses the tail.
- **A test that runs `git` drops the inherited `GIT_*` variables.** Inside a
  git hook (`make check` on pre-push), git exports `GIT_DIR` and friends, and
  a child `git` would act on the hook's repository instead of the test's
  temporary one.

## Consequences

- These entries are contracts with a dependency, not with users: each one is
  a place a cobra or pflag upgrade can silently change behaviour, and the
  registry and exit-code tests in `cmd/gplay` are what catch it.
- The user-facing rules they protect stay in DESIGN §9; an entry here can be
  deleted once the library stops needing the workaround, without any change
  there.
