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

## Known limits

These are by design, so they aren't vulnerabilities on their own:

- A browser session can't be revoked before it expires. The `studio_session` cookie is
  stateless and lasts 30 days, and the web app has no logout. Stealing it takes access to
  the user's browser profile.
- The one-time login ticket shows in `studio.log` (mode 0600) and in the arguments of the
  command that opens the browser. It works once, for ten minutes, on a server that answers
  only loopback names.

See [docs/api.md](docs/api.md#auth) for how Studio authenticates.

## Supported versions

Studio is pre-1.0: only the latest release gets security fixes.
