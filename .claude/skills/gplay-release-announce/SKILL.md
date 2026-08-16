---
metadata:
  harness: [claude]
  machines: both
  scope:
    projects: [google-play-cli]
name: gplay-release-announce
description: Schedule a gplay release-announcement thread for X + Threads on Typefully, both brand visuals attached. Use when announcing a gplay release or update, scheduling a release post to Typefully/X/Threads, picking a posting slot for a release, or writing a publication-v<version>.md.
---

# gplay-release-announce

Turns a live GitHub Release into a 2-message thread **scheduled** on Typefully at
a **slot** you approve, both visuals uploaded and attached. It schedules, it
never fires now: you pick the slot, Typefully posts it.

Visuals come from composing `gplay-social-cards` — don't reinvent rendering.
Everything Typefully goes through its MCP (the `typefully_*` tools; the names
below drop that prefix).

## Steps

1. **Target.** `gh release list --repo PollyGlot/google-play-cli -L 5` → latest tag. Confirm if unclear.
2. **Verify it's live.** `gh release view <tag> --repo PollyGlot/google-play-cli`. A draft → stop: a published release is a precondition, not a step.
3. **Angle.** `git log <prev-tag>..<tag> --oneline` + the release-please PR / CHANGELOG. Pick ONE headline user-facing feature; that angle carries the whole thread.
4. **Two visuals** via `gplay-social-cards` (1600×900). Read each PNG back to check version, fonts, alignment:
   - `release` card → `release-v<version>`: version focal + 2–4 bullets. Version visible (ADR-0010).
   - `terminal` demo → `terminal-<feature>`: the angle's real command flow, no version chrome (evergreen, ADR-0010). Align table output with `scripts/align_table.py`.
5. **Draft the thread — 2 messages** (same text on X & Threads):
   - **Msg 1** — announce + a plain `↓` (the char, not `⬇️`). No link. Carries the `release` card.
   - **Msg 2** (the reply) — technical caption + the repo link. Carries the `terminal` demo.
   - **Count both messages** including emoji and link: X ≤ 280 (no premium), Threads ≤ 500. Msg 2 usually overruns X → drop its rationale or split it to a third post *before* any API call.
6. **Schedule on Typefully.** Done when `get_draft` shows the draft `scheduled`, at the chosen slot, with **both images attached** (msg 1 = release card, msg 2 = terminal) — not before.
   - **Resolve target.** `list_social_sets` → confirm `312931` / @PollyGlot.
   - **Upload both visuals first**, so the scheduled draft is never imageless. Per PNG: `create_media_upload(file_name)` → raw PUT the bytes from the Mac → poll `get_media_status` until ready.
   - **Find the slot.** `get_queue_schedule` = the recurring windows; `get_queue` over the next ~7 days = concrete slots, where `draft: null` is free and `at` is the UTC time. `list_drafts status=scheduled` → an existing `Release v<version>` draft gets `edit_draft`, not a twin.
   - **Ask which slot** (AskUserQuestion): the next free slots rendered in `Europe/Paris`, plus a `next-free-slot` option. A wanted slot that's taken → ask whether to bump the sitting draft or pick another. Evening (19:00 Paris) = hot-take; keep ≥ 3 h spacing on X.
   - **Create it in one `create_draft` call**: X + Threads both `enabled`, each carrying the same two posts with their `media_ids`, `publish_at` = the chosen slot, `draft_title: "Release v<version> [auto]"`.
7. **Emit** `~/Documents/gplay-marketing/publications/publication-v<version>.md` as the trace: both messages, the image plan, the scheduled slot, and the draft URL.

## Rules (locked — don't re-litigate)

- **Honesty.** Every shown behaviour (commands, flags, columns, WARN, output) is verified in the code. Demo data = format-fidelity samples: `com.example.*`, BCP-47 locales (`en-US`, never `en_US`), fake `gp:…` / `GPA.…` IDs, zero real PII.
- **No em dashes in the post text** — a period, comma, or colon instead. (The ban covers the published posts, not this file's prose.)
- **Link = the repo** `github.com/PollyGlot/google-play-cli`, in **msg 2 only**. Never `gplay.sh/install` (dead, ADR-0009).
- **Voice.** Builder-honest, "I drive the agents, I decide." No hashtags, no "star the repo", respect incumbents — Fastlane/GPC jabs stay in standby comment drafts, never in the thread.
- **Slots come from the live queue**, never a memorized list. **Schedule, never publish now.**
- **Unschedule, don't delete.** Typefully has no archive, so a delete is final: a thread that shouldn't ship goes back to `draft` with a title saying why.

## Typefully mechanics (reference)

- **The MCP covers the whole path**, `create_draft` and `edit_draft` included (verified 2026-08-03). [`scripts/typefully_publish.sh`](scripts/typefully_publish.sh) is only for a session with no Typefully MCP connected: REST v2 + `Authorization: Bearer $TYPEFULLY_API_KEY` (never commit the key).
- `social_set_id` `312931` = @PollyGlot, timezone `Europe/Paris`.
- **A thread is one draft.** `platforms.x` and `platforms.threads` each take the full `posts` array — no top-level `posts`. Array order is the reply chain (msg 2 replies to msg 1); each post owns its `media_ids`.
- For `publish_at`, pass a queue slot's `at` (UTC, ends in `Z`) verbatim or the string `"next-free-slot"` — both sidestep offset math.
- **Raw PUT only** for the S3 upload: no `Content-Type`, no `Authorization` — the presigned signature omits them, so any added header → `403 SignatureDoesNotMatch`. `curl -T <file> "<url>"` (not `--data-binary`); `200`/`204` = success. This upload is *why* the skill runs on the Mac: the Cowork sandbox blocks the S3 PUT.

## Source of truth (read, don't duplicate)

- Style invariants (em-dash ban, demo data, voice) → `CLAUDE.md` Social + `~/Documents/gplay-marketing/skill-additions-claude-code.md`
- Voice, playbook, standby comment drafts → `~/Documents/gplay-marketing/publications/publication-thread-alpha2.md`
- Worked example (v0.2.0) → `~/Documents/gplay-marketing/publications/publication-v020.md`
- Post templates → `~/Documents/gplay-marketing/planning/templates.md`
- Cadence + which feature to spotlight → `~/Documents/gplay-marketing/planning/calendar.md`
