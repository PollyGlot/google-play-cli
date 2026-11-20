# ADR-0056: Media upload protocol: resumable for artifacts, simple for small payloads

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

Every file gplay sends to Play rides Google's upload sub-host, in one of two
protocols Discovery lists for each upload method: simple media
(`uploadType=media`, one POST carrying the bytes) or resumable
(`uploadType=resumable`, a session the client can resume after a failure).
PRD #355 moved the large artifacts onto resumable; the rest stayed simple.
The rules both paths follow lived in code comments; the comment purge (the
`scripts/lint/comments.py` ratchet) gathers them here. User-visible bounds
(chunk deadline, resume backoff) are in DESIGN §8, Request timeouts.

## Decision

### Which protocol

- **Resumable, unconditionally, for artifacts**: `edits.bundles.upload`,
  `edits.apks.upload`, `edits.deobfuscationfiles.upload`,
  `edits.expansionfiles.upload`, `accounts.customApps.create`, and the hosted
  app uploads (`appstoreappsreview` APK, image, policy declaration). No size
  threshold and no flag: a small file is a single chunk.
- **Simple media for Store images and Internal App Sharing**
  (`edits.images.upload`, `internalappsharingartifacts.uploadapk` and
  `uploadbundle`). PRD #355 left them out as small payloads with no confirmed
  resumable path; the current snapshot lists resumable for both, so moving
  them is open, not refused.

### Resumable: the protocol

1. **Initiate**: a POST to the method's upload URL with
   `uploadType=resumable`, `X-Upload-Content-Type` and
   `X-Upload-Content-Length`. The body is empty, except where the method takes
   resource metadata alongside the media: `customApps.create` (the app's
   title and language) and the app store policy declaration upload (its
   `fileType`) send it as JSON in this request. The method's query parameters ride
   this request only; the server answers with a session URI in `Location`,
   which already embeds them, and every chunk PUT goes there.
2. **Upload**: PUTs of a fixed chunk size with
   `Content-Range: bytes {start}-{end}/{total}`. An intermediate chunk is
   acknowledged with `308` and a `Range` header naming the last stored byte;
   the final chunk returns `2xx` with the resource, passed through verbatim
   (ADR-0003).
3. **Resume**: after a transient failure (a transport error or a `5xx`), a PUT
   with `Content-Range: bytes */{total}` and an empty body asks which byte the
   server last committed, and the upload continues from there.
4. **A zero-length upload** has no byte range to name, so its single PUT
   carries `Content-Range: bytes */0`.

### Resumable: what the client believes

- **The server's acknowledged offset is the only truth.** No byte past it is
  re-sent, and none before it is assumed stored.
- **A `308` without `Range` means zero bytes committed.**
- **An acknowledged offset outside `[0, size]` fails the upload.** `Range` is
  data from the network; trusting an out-of-bounds offset would compute a
  negative chunk length.
- **A probe that finds the upload complete** (the final chunk landed, its
  response was lost) returns that resource without another PUT.
- **The chunk size is 8 MiB, fixed.** Google rejects an intermediate chunk
  that is not a whole multiple of 256 KiB; 8 MiB is one, and PRD #355 kept it
  out of the CLI surface.
- **The helper owns retries for the transfer.** Chunk PUTs and probes run
  outside the `--retry` middleware (`transport.WithoutRetry`), which would
  otherwise double-send them. Recovery waits on the `--retry` backoff curve,
  and stops after 8 consecutive failures without the committed offset
  advancing: with the middleware off, this counter is what ends a loop
  against a server that keeps failing without accepting bytes. Progress resets
  it, so a genuine resume is never cut.
- **Each chunk has its own deadline** (5 minutes, a floor of about 28 KB/s).
  A connection that stops moving bytes without a reset never errors on its
  own; the deadline turns it into a probe and a resume, while a slow but
  progressing link is never cut.
- **A short read of the local file fails the upload**, even a clean `io.EOF`:
  the file no longer has the bytes the chunk promises, and sending the
  zero-filled rest of the buffer would corrupt the artifact server-side. It
  is a client-side error (exit 20).

### Resumable: the upload URL

- **The Schema index stores one upload path per method**, Discovery's
  simple-protocol `mediaUpload` path. The resumable path is the same one under
  a `/resumable` prefix and gplay appends `uploadType=resumable` itself, so
  storing both would keep two values that must agree.
- **The upload path is stored verbatim, not derived from the method path.**
  It has a different prefix (`upload/androidpublisher/v3/...`), not a suffix
  of the method path, so `releases upload`, the mapping upload and the image
  uploads all read it from the index.

### Simple media: the request

- **The body is a stream with an explicit `ContentLength`**, reopened from the
  file on each attempt. Without a length Go sends chunked
  `Transfer-Encoding`, which some upload front-ends handle poorly and which
  blocks transport retries; reopening per attempt lets a retry or a redirect
  replay the body (`net/http` closes `Request.Body` after each attempt).
- **Images announce their sniffed type**, never `application/octet-stream`
  ([ADR-0055](./0055-play-api-behaviours-the-client-absorbs.md),
  [ADR-0013](./0013-image-slot-reconciliation.md)).

### Both protocols: the local file

- **Only a regular file is uploaded.** A directory, fifo or device passes
  `Open` and `Stat` but cannot be streamed and reports a wrong size, so it is
  refused up front as a client-side error (exit 20), never surfacing as a
  transport failure (exit 50). A symlink to a regular file is accepted: the
  open follows it, so the size checked is the target's.

## Consequences

- One helper (`api.ResumableUpload`) carries the protocol for every resumable
  surface, tested once against a mocked transport.
- A move of images or Internal App Sharing to resumable changes no CLI
  surface; it is a client decision under this ADR.
