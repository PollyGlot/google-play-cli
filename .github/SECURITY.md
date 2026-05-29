# Security Policy

## Supported versions

`gplay` is pre-1.0. Only the **latest released version** receives security
fixes. Once 1.0 ships, this policy will be updated with an LTS column.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security reports.**

Two private channels are available, pick whichever is convenient:

1. **GitHub Private Vulnerability Reporting** —
   [open an advisory](https://github.com/PollyGlot/google-play-cli/security/advisories/new).
   Preferred — keeps the conversation in the repo with proper coordination.
2. **Email** — `paul.trinko95@gmail.com`. Use the subject line `[gplay
   security] <short summary>`.

Please include:

- The version of `gplay` affected (output of `gplay version`).
- A description of the vulnerability and its impact (what an attacker can do).
- Reproduction steps or a proof-of-concept.
- Any suggested remediation.

You'll get an acknowledgement within **72 hours** and a status update within
**7 days**. Coordinated disclosure is the default; we'll agree on a public
disclosure date together once a fix is ready.

## Scope

In-scope:

- The `gplay` CLI binary and its source.
- Build/release tooling that ships artifacts users install.

Out of scope (please file with the relevant project instead):

- Vulnerabilities in the Google Play Developer API itself — report to Google.
- Vulnerabilities in upstream Go dependencies — report to the upstream
  project; we'll bump as soon as a fix lands.

## Credit

Reporters who follow this policy will be credited (by handle of their choice)
in the release notes of the version containing the fix, unless they prefer to
remain anonymous.
