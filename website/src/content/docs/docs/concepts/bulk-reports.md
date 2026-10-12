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
  rows of one app, matched on the package column of the CSV header (the
  finance reports call it *Product ID*); the Project pin does not apply, so
  without `--package` you get every app. The earnings file name may carry a
  suffix after the month: gplay reads every file of the month, whatever its
  suffix.

`gplay reports list` shows every family, month and dimension the service
account can see, with each file's size and last update, and the product id
of each subscription statistics file.

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
transparently.

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
header text, not their position: a header with the word *buyer* and a place
below the country (city, state, province, postcode, postal, zip) is hidden,
so the masking survives a Google rename. CI logs and agent contexts thus
never collect a buyer's location by accident.

## ZIP bounds

The finance exports are ZIP archives from a bucket gplay does not control.
gplay reads them under fixed bounds and refuses, with exit `20`, any export
that crosses one, rather than return a truncated report:

| Bound | Value |
| --- | --- |
| Compressed object | 128 MiB, checked on the listed size before the fetch |
| Members in the archive | 16 |
| Decompressed bytes per member | 1 GiB |
| Decompressed bytes in total | 1 GiB |

Only the `.csv` members are read, each decoded by its byte-order mark (UTF-16
or UTF-8). A member's declared size is checked before anything is inflated,
and the inflated bytes are counted too, since a declared size can lie.

## Related

- [`gplay reviews history`](/docs/guides/reviews/) reads the reviews export
  of the same bucket.
- [Vitals](/docs/reference/vitals/) has a real API for crash and ANR rates;
  the `crashes` export is the monthly count.
