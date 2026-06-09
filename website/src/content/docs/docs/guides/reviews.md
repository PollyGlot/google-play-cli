---
title: Reviews
description: Read recent Google Play user reviews and reply to them with gplay — star filtering, JSON pass-through, and batch replies from a TSV file or stdin.
sidebar:
  order: 4
---

## Listing reviews

```sh
gplay reviews list                       # most recent reviews
gplay reviews list --stars 1-2           # only 1- and 2-star
gplay reviews list --stars 1,3,5         # a set
gplay reviews list --limit 20            # cap the count
```

:::caution[The 7-day window]
The Google Play Developer API only exposes reviews from the **last 7 days**.
gplay prints a `WARN` line to stderr on every run — including an empty
result, so "no output" never silently reads as "this app has no reviews".
Long-history retrieval (Google's CSV reports in Cloud Storage) is on the
roadmap.
:::

`--stars` filters **client-side** (the API has no server-side rating
filter), results auto-paginate until exhausted, and `--output json` is the
`{"reviews": [...]}` pass-through reflecting the filtered set.

## Replying

```sh
gplay reviews reply --review-id REVIEW_ID --reply "Thanks for the feedback!"
```

## Batch replies

For support workflows (or an agent drafting replies for approval), `reply`
takes a TSV stream — one `<review-id>\t<reply text>` line per reply, `-` for
stdin:

```sh
gplay reviews reply --batch replies.tsv
gplay reviews list --stars 1-2 --output json | my-draft-tool | gplay reviews reply --batch -
```

Blank lines and `#` comments are skipped; replies containing tabs or
newlines use RFC 4180 double-quoting. Each line posts sequentially — a
failing line is reported on stderr and does **not** abort the rest, and the
process exits with the highest exit code seen.

Use `--dry-run` to parse and print the planned replies without calling the
API.

## Permissions

Replying requires the *"Reply to reviews"* permission on the service
account in the Play Console — `gplay auth doctor` and the
[service account setup](/docs/getting-started/service-account/) page cover
granting it.

## Related

- [`gplay reviews` reference](/docs/reference/reviews/)
- [Output formats](/docs/concepts/output-formats/)
