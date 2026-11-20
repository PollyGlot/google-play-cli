# ADR-0060: Path containment implementation

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

DESIGN §8 "Path containment" sets the user contract: every path built from a
tree a repo owns (a metadata or images tree, a release-notes directory,
`.gplay/`) must resolve at or under its root, the operator's own paths can be
opened up with `GPLAY_ALLOW_EXTERNAL_SYMLINKS`, and paths derived from API
data never can. This ADR records how the codecs keep that contract, which §8
leaves to the implementation, and repeats none of it. These decisions lived in
code comments; the comment purge (the `scripts/lint/comments.py` ratchet)
gathers them here.

## Decision

### The root

- **The root is resolved once per walk**, and every derived path is checked
  against it, rather than each path resolving its own idea of the root.
  Resolving it per file would cost a syscall per file and, worse, let the root
  itself be swapped mid-walk. The one-shot operations on `.gplay/` (a single
  pin read, write or removal) resolve it per call, since there is no walk to
  protect.
- **The root is resolved after the directory listing.** A missing directory
  then reports the familiar not-found error, not a containment failure.
- **The write side creates the root first.** `pull` writes into a tree that
  may not exist yet, and containment needs a real directory to resolve.
- **An empty tree writes nothing and creates nothing.** `pull` on an app with
  no listings or no images returns before establishing the root, so it leaves
  no empty directory behind.

### The check

- **Both sides are resolved through `EvalSymlinks`**, then the resolved
  candidate must be at or under the resolved root. "Under" is decided with
  `filepath.Rel`, not a string prefix, so `/repo/.gplay-evil` is not taken
  for a child of `/repo/.gplay`.
- **A path that does not exist yet is checked through its nearest existing
  ancestor**, which is resolved, with the missing components re-appended
  lexically. That is sound because a component that does not exist cannot be
  a symlink: an escape has to come from an ancestor that does exist.
- **The caller gets back the path it named**, not the resolved one: the name
  has just been proved to land inside the root, and returning the resolved
  form would silently rewrite the file names in the caller's own error
  messages.
- **An escape is a usage error (exit 2) naming the path**: a bad input, not a
  transient failure, so a retry is pointless and the message must say which
  path to fix.
- **The strict check names the escape hatch only to say it does not apply.**
  An operator who set `GPLAY_ALLOW_EXTERNAL_SYMLINKS` and is still refused
  would otherwise conclude it is broken; the truth is that the path was built
  from API data, which the hatch never covers.
- **Under the hatch, a symlink below the root is told apart from a `..`
  above it** by asking whether some ancestor of the path, as spelled, resolves
  to the root. Ancestors are compared in resolved form, not as strings, so the
  test holds when the root itself is reached through a link (macOS
  `/var -> /private/var`). The walk costs one `EvalSymlinks` per level, which
  is acceptable because it only runs on the escape path, under an opt-in.
- **The `NOTE:` stream is a package-level sink**, set once by `cmd/gplay` to
  the redacting stderr, rather than a parameter: the codecs that use the
  relaxable check take a directory and nothing else, and threading a writer
  through all of them for a condition that is off by default would cost every
  signature.

### Names that become paths

- **A name joined into a path is one plain path component.** A package name
  (`.gplay/edit-<pkg>.json`) and an Account name (`<keystore>/<name>.json`)
  arrive from flags, environment variables and repo files, and nothing
  upstream forbids a separator or a `..`. Android package names are
  filesystem-safe when well formed; that is an assumption about the input,
  not a guarantee, so the component check runs before any I/O.
- **Once the component check passes, a lexical join is safe** even where the
  directory does not exist yet (a first `edits begin` creating `.gplay/`):
  with no separator in the name there is no traversal to perform.
- **The unchecked join is for messages only.** The pin path rendered in an
  error is the plain join; every path used for I/O goes through the checked
  helper.
- **A refused Account name deletes as not found.** Save never stores such a
  name, so there is nothing under it, and a caller that sweeps every store
  (`auth logout` deletes from the keyring and then the file backend) must be
  able to finish for a keyring-held Account whose name has a slash. The OS
  keyring keys items by name without touching the filesystem, so it keeps
  accepting any name.

### Reading

- **Containment comes before any touch of the file.** An escaping path is
  neither opened nor stat'd, so a refusal reveals nothing about whether its
  target exists, and the content is never loaded to be dropped later.
- **The walk does not descend into a symlinked directory entry.** The
  directory listing reports entries with `lstat` semantics, so a locale
  directory or an image gallery directory that is itself a symlink reads as
  a stray entry and is skipped, hatch or not (#843 tracks whether it should
  be followed or refused instead). Containment is the backstop for every
  path the walk does follow: a symlinked `images/` directory, an image file
  or a field file.
- **Names found on disk use the relaxable check; names from the API use the
  strict one.** The read side checks every name it found by listing the
  operator's own directory with the form `GPLAY_ALLOW_EXTERNAL_SYMLINKS`
  opens; the write side builds `<dir>/<locale>/` from the API's locale and
  stays strict whatever the environment says.

### Writing

- **A leaf is checked, not only its directory.** `os.WriteFile` follows a
  symlinked leaf, so a `title.txt` or `icon.png` pre-placed as a link would
  have its target overwritten by `pull`. The write helper refuses to write
  through any symlinked leaf: a link out of the root is already an escape,
  and a link back inside the root would still write a file the caller did not
  name.
- **A staging file is contained like its target.** The global config and the
  Edit pin are written to `<path>.tmp` and renamed over the target, so a crash,
  a kill or a full disk leaves the old file or the new one, never a truncated
  file that breaks every later command (a corrupt pin is refused by every
  command, `edits begin` included). `<path>.tmp` is a name nothing owns, and
  the `edit-*.json` rule `gplay init` gitignores does not cover it, so a repo
  can commit it as a symlink: the staging path goes through the write check
  too, against the same resolved root as the target. The leaf check is the
  one that catches a staging link aimed back inside `.gplay/` (at
  `config.json`, say). A failed rename removes the staging file; the edit
  pin also removes it when the write itself fails, while the global config
  save leaves a truncated `config.json.tmp` behind in that case.
- **`gplay init` checks both files before writing either.** `.gplay/config.json`
  and `.gplay/.gitignore` sit in a directory a cloned repo controls, so either
  can arrive as a committed symlink (#791); checking both first means a
  refusal leaves nothing half initialised. A symlinked `.gplay/` pointing out
  of the repo is caught the same way.
- **A root that will not resolve on a write means the in-memory filesystem**
  used by tests: there is nothing on disk to contain against.
- **An image is written to a temporary sibling, then renamed over the
  target**, so a copy that fails partway leaves the previous image whole. The
  temporary name ends in neither `.png` nor `.jpg`, so neither Read nor the
  gallery cleanup mistakes a leftover for an image.
- **Deletion is contained like writing.** The gallery cleanup of `pull`
  removes files; a `2.png` that is a symlink must not turn it into a removal
  outside the tree.

### Between the scan and the use

A scan describes the tree at one instant; a file opened later can have been
swapped since.

- **An image is reopened against its root every time it is read** (to hash
  it, to upload it). Containment is checked again after the open, and the
  opened file must be the one the contained name designates (`os.SameFile`),
  so a link swapped in between is refused rather than read.
- **The bytes uploaded are the bytes checked.** The pre-check and the plan
  each read the file on their own, so neither proves what an upload holds;
  the upload validates and hashes the very buffer it sends (DESIGN §4,
  Concurrent reads).
- **A release-notes file is size-checked twice**: stat'd against its cap
  before the read, and measured again after, against a file that grows in
  between.

### Tests

- **A test asserts the outcome, not the defence.** Where two defences stop
  the same escape (the `lstat` skip and containment), the test checks that
  nothing from outside the tree reaches the result; a refusal is an
  acceptable outcome, silently returning the outside file is not.
- **On macOS `t.TempDir()` sits under the symlinked `/tmp`**, so every test
  already exercises the "checkout reached through a symlink" case.

## Consequences

- Each codec (text tree, image tree, release notes, `.gplay/`) goes through
  the same path guard; a new tree reader inherits the rules by using it, not
  by restating them.
- §8 stays the user contract; a rule here may change as long as §8 holds.
