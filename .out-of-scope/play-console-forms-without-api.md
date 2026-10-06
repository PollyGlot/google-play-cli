# Play Console forms with no API

gplay does not cover the Play Console "App content" forms that the Google Play
Developer API does not expose: the IARC content-rating questionnaire, target
audience and children's content, ads declarations, and the other web-only
declarations (news, health, and so on).

## Why this is out of scope

gplay is an API-first CLI: it talks to the Developer API over raw HTTP
([ADR-0007](../docs/adr/0007-raw-http-not-google-go-sdk.md)) and nothing else.
These forms have no endpoint, so the only way to drive them would be browser
automation against the Play Console web UI. That is a different product, with
a different failure model (UI changes break it silently, it needs a human
login, it cannot run from CI with a service-account credential).

Data Safety is the exception that proves the rule: it *is* exposed
(`applications.dataSafety`), so gplay covers it.

If Google ever ships an endpoint for one of these forms, it is an ordinary
Discovery surface change and goes through normal triage under ADR-0026.

## Prior requests

- #115: "Parking: réglementaire Play Console hors Developer API"
