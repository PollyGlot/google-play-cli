# ADR-0060: Path containment implementation

## Status

accepted (2026-11-20)

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
- **The root is resolved after the directory listing.** A missing directory
  then reports the familiar not-found error, not a containment failure.
- **The write side creates the root first.** `pull` writes into a tree that
  may not exist yet, and containment needs a real directory to resolve.
- **An empty tree writes nothing and creates nothing.** `pull` on an app with
  no listings or no images returns before establishing the root, so it leaves
  no empty directory behind.

### Reading

- **Containment comes before any touch of the file.** An escaping path is
  neither opened nor stat'd, so a refusal reveals nothing about whether its
  target exists, and the content is never loaded to be dropped later.
- **The walk does not descend into a symlinked directory entry.** The
  directory listing reports entries with `lstat` semantics, so a locale
  directory that is itself a symlink reads as a stray entry and is skipped,
  hatch or not. Containment is the backstop for every path the walk does
  follow: a symlinked `images/` directory, gallery or field file.
- **Names found on disk use the relaxable check; names from the API use the
  strict one.** The read side checks every name it found by listing the
  operator's own directory with the form `GPLAY_ALLOW_EXTERNAL_SYMLINKS`
  opens; the write side builds `<dir>/<locale>/` from the API's locale and
  stays strict whatever the environment says.

### Writing

- **A leaf is checked, not only its directory.** `os.WriteFile` follows a
  symlinked leaf, so a `title.txt` or `icon.png` pre-placed as a link would
  have its target overwritten by `pull`. The write helper checks the file
  path itself before writing.
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
