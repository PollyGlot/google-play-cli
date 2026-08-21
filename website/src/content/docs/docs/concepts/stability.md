---
title: Stability and the Public contract
description: "What gplay 1.0 freezes and what it does not: the per-command stability label, what the Public contract covers, and how an experimental command graduates."
sidebar:
  order: 9
---

`gplay` 1.0 is a **stability promise, not a feature checklist**. It answers one
question: *can I wire this into CI without it breaking under me?*

The promise is **per command**, not global. A command is either part of the
frozen Public contract or explicitly outside it.

## The two states

| State | How you see it | What it means |
| --- | --- | --- |
| **Frozen** (no label) | nothing in `--help` | Part of the Public contract. Its name, flags, semantics and exit codes cannot change without a **major** version bump. |
| **`[experimental]`** | `[experimental]` in the command's `Short` and a notice at the top of its `--help` | Shipped and supported, but outside the contract. Flags, output and behavior may change in **any** release. |

Absence of a label means frozen. That default is deliberate: forgetting to mark
a command over-promises rather than under-promises, which is the failure mode
you can actually plan around.

To see the state of a command, ask the binary. It is the source of truth:

```bash
gplay releases --help
```

Experimental subcommands are tagged inline in the listing, so you can tell at a
glance before wiring anything into a pipeline.

## What the Public contract covers

**Covered**, where a breaking change requires a major bump:

- command and flag names, and their semantics
- exit codes (see [Exit codes](/docs/concepts/exit-codes/))
- the config schema and its resolution precedence
- Account resolution precedence
- the guarantee that `--output json` stays API pass-through

**Not covered**, free to change in a minor release:

- the `table` and `markdown` layouts (human-facing views)
- the **fields inside** the pass-through JSON. Those belong to the Google Play
  Developer API, not to gplay. See [Output formats](/docs/concepts/output-formats/).
  gplay promises that JSON *stays* pass-through, not that Google will never
  change a field.
- stderr wording and log output

That carve-out is what keeps the promise honest. Promising something you do not
control is a broken promise waiting to happen.

## What ships experimental today

As of 1.0:

- `gplay schema`
- `gplay orders`
- `gplay subscriptions`, `gplay iap`, the declarative monetization catalog
- `gplay games`
- `gplay recovery`, `gplay device-tiers`, `gplay customapps`
- `gplay appstore`, the alternative-app-store surface
- `gplay releases sharing`, `gplay releases expansion-files`, `gplay releases generated`
- `gplay reviews history`

Everything else is frozen: auth, apps, the core release loop, tracks, testers,
team, edits, metadata, compliance, vitals, and `reviews list` / `view` /
`reply`.

A few **sub-features** of otherwise frozen commands are also marked
experimental in prose, where a whole-command label would be too blunt: APK
(rather than AAB) upload on `gplay releases upload`, the icon fields on
`gplay apps view`, and `--type` on `gplay metadata images list`.

## How a command graduates

An experimental command becomes frozen once its shape has survived real use:
typically a full round-trip against a live app, and no open questions about its
flags. Graduation is **additive**: dropping the label is not a breaking change,
so it lands in a normal minor release.

The reverse never happens. A frozen command does not become experimental again;
if its surface has to change, that is a major bump.

## Depending on experimental surface

You can: it is shipped, tested, and supported. Just pin an exact release
rather than a range:

```bash
gplay version
```

and use that exact tag in your CI install step. When you upgrade, re-read the
release notes for the namespaces you depend on.
