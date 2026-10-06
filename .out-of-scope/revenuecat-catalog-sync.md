# RevenueCat catalog sync

gplay does not reconcile a RevenueCat project with the Play catalog: there is
no `gplay revenuecat` namespace.

## Why this is out of scope

gplay wraps one surface, the Google Play Developer API (and the Play Developer
Reporting API). RevenueCat is a third-party subscription backend with its own
API, its own credentials (a secret API key, not a Play service account) and its
own versioning. Syncing it means calling RevenueCat, not Google: a second
provider inside the CLI, a second contract to keep stable, and a second set of
secrets to store and resolve next to Accounts.

The useful half already ships. `gplay subscriptions pull` and `gplay iap pull`
write the Play catalog as declarative files
([ADR-0041](../docs/adr/0041-declarative-monetization-catalog.md)), and those
files are the input any RevenueCat sync needs: product identifiers,
types and display names are all in them. A sync is therefore a composition over
gplay's output plus RevenueCat's API, which is what an agent skill is for:

```bash
gplay subscriptions pull --dir ./monetization
gplay iap pull --dir ./monetization
# then: a skill (google-play-cli-skills) diffs ./monetization against the
# RevenueCat project and applies products, entitlements and offerings
```

The equivalent iOS workflow lives the same way, outside the store CLI.

## Prior requests

- #373: "PRD: RevenueCat catalog sync (reconcile Play catalog with RevenueCat)",
  with its slices #419, #420, #421, #422
