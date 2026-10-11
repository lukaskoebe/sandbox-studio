# Security policy

Sandbox Studio's job is to contain untrusted agents, so a way for a sandbox to reach the
network without passing the gateway, read a secret, push without review, control the
browser outside the broker's policy, or reach the Studio UI or API is a vulnerability.

## Reporting

Report vulnerabilities privately through GitHub:
[Security → Report a vulnerability](https://github.com/lukaskoebe/sandbox-studio/security/advisories/new).
Please don't open a public issue. Include the Studio version (`studio version`), the host OS,
and steps to reproduce.

The maintainer aims to reply within a week. Fixes are released as soon as they are ready, and the
advisory is published with the release, crediting you unless you prefer otherwise.

## Supported versions

Studio is pre-1.0: only the latest release gets security fixes.
