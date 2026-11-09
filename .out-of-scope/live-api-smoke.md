# Live API smoke test

No scheduled job runs gplay against the real Play API. Every automated check
is offline: the test suite drives a fake Play transport (`internal/testkit`),
and the Discovery watch tracks the published API description.

## Why this is out of scope

The idea came from two bugs only the live API showed: #731 ("Only one
completed release is allowed", a write path) and #741 (a legacy list refused
with "Please migrate to the new publishing API" for apps Google moved to the
v2 catalog). A read-only smoke against one app would have caught neither for
sure: the first is a write, the second needs the smoke app to be in the
affected state. A write smoke needs a sandbox app, an AAB build and a rising
versionCode per run.

Against that, both bugs were reported by users within days, and the fix
landed with an offline test that pins the live behaviour. The upkeep (a
service-account secret in a public repository, a weekly job to watch) does
not pay for itself. A read-only version shipped in #749 and was removed
before it was ever configured.

## Prior requests

- #736: "Parking: live smoke test of the release flow against a sandbox app"
