# Reviews history beyond the 7-day window: GCS CSV reports (`reviews history`)

Grilled from PRD [#94](https://github.com/PollyGlot/google-play-cli/issues/94)
after its investigation spike (2026-07-08). `reviews list` is capped by the
API: `reviews.list` only returns **the last 7 days**. The official channel for
the full history is Google's **monthly CSV reports** in the developer's
reporting bucket:

```
gs://pubsite_prod_rev_<developer-id>/reviews/reviews_<package>_YYYYMM.csv
```

Spike findings (confirmed against Play Console Help “Download and export
monthly reports”):

- **Encoding is UTF-16** (Google documents the BigQuery import as “convert
  from UTF-16 to UTF-8”) — a transcoding step is mandatory, not defensive.
- **Schema is stable and documented**: 16 columns (Package Name, App Version
  Code/Name, Reviewer Language, Device, Review Submit Date/Millis, Review Last
  Update Date/Millis, Star Rating, Review Title, Review Text, Developer Reply
  Date/Millis/Text, Review Link); most are optional.
- **Auth**: the *same* service account, granted “View app information”
  (global) in the Play Console, reads the bucket with OAuth scope
  `https://www.googleapis.com/auth/devstorage.read_only`.
- **Bucket naming**: the Console's “Copy Cloud Storage URI” button is the
  authoritative source; in practice the suffix is the numeric developer
  account ID — an axis gplay already addresses
  ([ADR-0015](0015-developer-account-addressing-rides-on-account.md)).

The decisions:

- **Surface: `gplay reviews history`, a sibling of `reviews list` — not a
  flag on it.** Different data channel (GCS objects vs Android Publisher),
  different auth scope, different freshness (monthly files vs live API), and a
  different natural axis (`--month`). Folding it into `reviews list --since`
  would make one command silently switch backends — the opposite of the
  repo's explicitness conventions. The permanent 7-day `WARN` on
  `reviews list` gains a pointer to `reviews history`.

- **GCS is a third service, reached like the second one.** Raw HTTP
  ([ADR-0007](0007-raw-http-not-google-go-sdk.md)) against the GCS JSON API
  (`storage.googleapis.com/storage/v1/b/{bucket}/o` to list,
  `?alt=media` to fetch), with the extra `devstorage.read_only` scope
  requested the way vitals added `playdeveloperreporting`
  ([ADR-0027](0027-vitals-second-service-scope-readonly.md)). Read-only by
  scope construction — no mutating surface exists here.

- **Bucket derived, override honest.** Default bucket is
  `pubsite_prod_rev_<developerId>` from the existing developer-account axis;
  `--bucket` overrides it for accounts whose Console-issued URI differs. A
  404/403 error message tells the user where in the Console to copy the real
  URI and which permission the service account lacks.

- **CSV is parsed, and `--output json` deviates from pass-through — there is
  no API JSON to mirror.** Rows are decoded (UTF-16 → UTF-8, header-driven)
  into objects with stable lowerCamel field names derived from the documented
  headers. This is the same class of documented deviation as the binary
  download in [ADR-0034](0034-generated-apks-binary-download-to-file.md):
  [ADR-0003](0003-json-passthrough.md) governs API-mirroring commands, and
  this command's upstream is a CSV file, not a JSON response.

- **Month addressing: `--month YYYY-MM`, defaulting to the latest available
  month.** Range aggregation (`--from`/`--to`) is a follow-up slice, not v1.

- **Reviews only.** The same bucket serves stats/vitals/sales exports; those
  stay out of scope (vitals has a real API — [ADR-0027](0027-vitals-second-service-scope-readonly.md);
  stats/sales exports are their own future decision). The GCS client is
  written resource-agnostic so a future surface reuses it.
