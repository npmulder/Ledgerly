# Security Policy

Ledgerly is bookkeeping software. Beyond the usual vulnerability classes (auth bypass, injection, XSS, …), we treat **data-integrity defects as security-relevant**: anything that lets money math go wrong, lets ledger rows be mutated or deleted, or lets postings drift from their source facts. If you find one of those, report it here too.

## Supported versions

Ledgerly is pre-release. Only the `main` branch is supported; there are no versioned releases yet.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security problems.**

Report privately via [GitHub Security Advisories](https://github.com/npmulder/Ledgerly/security/advisories/new) on this repository.

Please include:

- A description of the issue and its impact
- Steps to reproduce (a proof of concept helps a lot)
- Any suggested remediation, if you have one

## What to expect

- Acknowledgement within **7 days**.
- An assessment and remediation plan, or a request for more detail, within **14 days** of acknowledgement.
- Credit in the fix's release notes if you'd like it (tell us how you want to be credited).

Please give us reasonable time to fix the issue before any public disclosure.
