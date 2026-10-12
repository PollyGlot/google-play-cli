---
title: Bulk reports
description: "What gplay reports reads: the monthly statistics and financial exports Play deposits in your Reporting bucket, the permissions they need, when they appear, and the output shape."
sidebar:
  order: 10
---

Install counts, ratings over time, Store listing acquisitions and revenue have
no JSON API. Play publishes them once a month as **Bulk reports**: CSV (and,
for finances, ZIP) files in your developer account's **Reporting bucket**,
`gs://pubsite_prod_rev_<developerId>/`. `gplay reports` reads them for you:
no `gsutil`, no UTF-16 transcoding by hand.

```sh
gplay reports list --package com.example.app        # what exists
gplay reports installs                               # latest month, overview
gplay reports installs --month 2026-09 --dimension country
gplay reports installs --since 2026-01 --until 2026-06 --dimension country --output json
gplay reports store-performance --dimension traffic_source
gplay reports subscription-stats --product premium_monthly
gplay reports earnings --month 2026-09 --package com.example.app
gplay reports sales --output json
```

The whole `reports` group is `[experimental]`: the columns are Google's, so
gplay cannot freeze them (see [Stability](/docs/concepts/stability/)).

## Families, months and dimensions

A Bulk report is identified by three things:

- **Family**: what it measures, one command each: `installs`, `ratings`,
  `crashes`, `store-performance` and `subscription-stats` per package;
  `earnings` and `sales` (estimated sales) for the whole developer account.
- **Month**: one file per calendar month. Without `--month`, gplay reads the
  latest month present for the package; `--since` and `--until` read a range
  and merge it into one result set.
- **Dimension**: per-package statistics come as one file per split
  (`country`, `device`, `app_version`, `os_version`, `language`, `carrier`,
  and an `overview` file). `--dimension` picks it. gplay checks the value
  against what the bucket holds, not against a list of its own, so a
  dimension Google adds works the day it appears. Without `--dimension`,
  `overview` is read when present; otherwise the error names the dimensions
  that exist for that month.

Two families differ:

- **Store performance** comes as one file per breakdown, `country` or
  `traffic_source` (the latter with the search term and UTM fields), and has
  no `overview` file: pass `--dimension`. Its cells are quoted, and a search
  term holding a comma, a tab or a line break comes out as typed.
- **Subscription statistics** are per subscription product:
  `--product <id>` is required, and the export is split by `country` (read by
  default). A product id may contain underscores; gplay reads the object name
  from the package, the month and the final dimension, so
  `premium_monthly_v2` is never cut short.
- **Earnings and estimated sales** cover every app of the developer account,
  one ZIP per month holding the CSV, with no dimension. `--package` keeps the
  rows of one app, matched on the package column of each file's own CSV
  header (*Package Name*, *Package ID*, or *Product ID* as the finance
  reports call it), so a range across a rename of that column keeps every
  month. The Project pin does not apply, so without `--package` you get every
  app. When `--package` keeps none of the rows read, a stderr note says so
  (`0 of N rows matched --package X`) and the exit stays `0`; a file with no
  header at all is an empty report. The earnings file name may carry a
  suffix after the month: gplay reads every file of the month, whatever its
  suffix. A file named `.csv` instead of `.zip` is read as the CSV it is.

`gplay reports list` shows every family, month and dimension the service
account can see, with each file's size and last update, and the product id
of each subscription statistics file. The dimension column is for the
per-package families only: an earnings name suffix is no dimension, and the
object name shows it.

## Permissions and the bucket

gplay reads the bucket over the Cloud Storage API with the same service
account, minting a separate read-only storage token. The service account needs
a **global** permission in the Play Console:

| Family | Permission |
| --- | --- |
| Statistics (installs, ratings, crashes, Store performance) | *View app information* |
| Subscription statistics | *View financial data* |
| Finances (earnings, estimated sales) | *View financial data* |

A `403` names the missing permission. The bucket name defaults to
`pubsite_prod_rev_<developerId>`; when your Console-issued URI differs, copy
it with the **Copy Cloud Storage URI** button on the Console's *Download
reports* page and pass its suffix with `--bucket`. `reports list` turns a
`403` on one family into a warning, so a service account without the
financial grant still sees its statistics.

## Freshness

Statistics cover daily rows, published as a monthly file 3 to 7 days after
the month ends. Earnings land around the 5th of the following month, and
estimated sales are low latency. A month that is not published yet is
"no report": exit `30`, with a message naming the family, the package (for a
per-package family) and the bucket. Inside a range, a missing month is
skipped with a warning.

Play occasionally republishes an export: a file the listing has just named
can be gone when gplay reads it. gplay then says so and asks you to run the
command again (exit `30`); inside a range, that month is skipped with a
warning like an unpublished one.

## Output

The columns come from each file's CSV header, never from a list in gplay:
a column Google adds appears in your output without a gplay upgrade.

```json
{
  "report": {
    "family": "installs",
    "package": "com.example.app",
    "months": ["2026-09"],
    "split": "country",
    "objects": ["stats/installs/installs_com.example.app_202609_country.csv"]
  },
  "columns": [
    {"key": "date", "header": "Date"},
    {"key": "country", "header": "Country"},
    {"key": "dailyDeviceInstalls", "header": "Daily Device Installs"}
  ],
  "rows": [
    {"date": "2026-09-01", "country": "FR", "dailyDeviceInstalls": "12"}
  ]
}
```

- **Keys** derive from the header: the words are lowerCamel-joined, so
  `Daily Device Installs` becomes `dailyDeviceInstalls` and `Daily ANRs`
  becomes `dailyAnrs`. A repeated header gets a numeric suffix (`installs2`).
- **Values** are the CSV's strings, verbatim: no number or date parsing, so
  postcodes and product ids keep their leading zeros.
- **Ranges** merge by the union of columns: a month that predates a column
  simply has no key for it in its rows.
- The **table** shows every column but a few per family (the package name,
  rarely read counters); `--columns` picks any key.

UTF-16 and UTF-8 files, with or without a byte-order mark, are decoded
transparently. A UTF-16 file with an odd number of bytes was cut mid
character: gplay refuses it (exit `20`) rather than drop the last byte.

## Buyer location

Earnings and estimated sales carry the buyer's country and sometimes their
city, state and postcode. gplay hides the city, state and postcode by default,
from the table and the JSON alike, and keeps the country (revenue per country
is the common aggregate and locates no one). A stderr notice says how many
columns were hidden and names the flag that shows them:

```text
NOTE: 3 buyer-location columns hidden (City of Buyer, State of Buyer, Postal Code of Buyer): pass --include-buyer-location to show them
```

`--include-buyer-location` shows them. The columns are recognised from their
header text, not their position: a header naming the *buyer* and a place
below the country (city, state, province, postcode or post code, postal,
zip) is hidden, whatever separates the words (`Buyer City`, `BuyerCity`,
`cityofbuyer`), so the masking survives a Google rename. A header naming the
country alone stays; one naming the country next to a place
(`Country / City of Buyer`) is hidden for the place. CI logs and agent
contexts thus never collect a buyer's location by accident.

## Bounds

The exports come from a bucket gplay does not control, and a range can span
years. gplay reads them under fixed bounds and refuses, with exit `20` and an
error naming the bound, anything that crosses one, rather than return a
truncated report or exhaust memory:

| Bound | Value |
| --- | --- |
| Compressed finance object | 128 MiB, checked on the listed size before the fetch |
| Members in a finance archive | 16 |
| CSV read by one command | 512 MiB, every month of a range together |
| Cells held by one command | 24 Mi, every month together; a row counts as its cells plus two |

The last two bounds are per command, not per file: a range that crosses one
fails as a whole, and a narrower `--since/--until` reads. Rows that
`--package` drops are never held, so they count towards the bytes read only.
At the cell bound, gplay holds about 700 MB of rows whatever their shape.

Only the `.csv` members of an archive are read, each decoded by its
byte-order mark (UTF-16 or UTF-8). A member's declared size is checked
against what is left of the budget before anything is inflated, and the
inflated bytes are counted too, since a declared size can lie.

## Related

- [`gplay reviews history`](/docs/guides/reviews/) reads the reviews export
  of the same bucket.
- [Vitals](/docs/reference/vitals/) has a real API for crash and ANR rates;
  the `crashes` export is the monthly count.
