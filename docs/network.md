# Network, secrets and CA

Every TCP connection from a sandbox, including from containers inside it, goes through
Studio's gateway. Code: `internal/gateway` (SOCKS5, naming, interception), `internal/policy`
(rules and hold-and-ask), `internal/dnsproxy` (per-sandbox resolvers), `internal/caddyrule`
(Caddy rules), `internal/secrets` (vault), `internal/ca` (per-environment CA).

## The fence

microsandbox's own policy, fixed at creation (see [sandboxes](sandboxes.md#network)):
DNS only to the sandbox's resolver, TCP only to public addresses and only through the
gateway's SOCKS5 port, everything else denied. UDP is blocked, so QUIC falls back to TCP.
Private and host addresses are unreachable; host services are virtual hosts (below) or the
[agent channel](guest-agent.md).

## DNS

Each sandbox has its own loopback resolver (UDP and TCP, `dnsproxy`), so names can be mapped
back per sandbox. It forwards to the host's system resolvers and records A/AAAA answers for
IP→name lookups. It doesn't deny names; policy is applied at connect time.

## Gateway pipeline

Per connection (`gateway.go`):

1. Identify the sandbox from its SOCKS5 user (the sandbox ID) and password (derived from
   the gateway key, which only Studio and microsandbox know).
2. Name the destination: the SOCKS domain if the client sent one, else the sandbox's DNS
   map, else sniff TLS SNI or the HTTP Host header. Sniffing waits up to 5 s on ports 80 and
   443, 300 ms when the name is already known, 1 s otherwise, because some protocols (SSH)
   wait for the server to speak first.
3. Names under `*.studio.internal` (DNS answer `198.18.0.1`) are virtual hosts served by the
   gateway itself, e.g. `git.studio.internal` for [git review](git-review.md).
4. Ask the policy (below). Without a decision, the connection is held.
5. Act:
   - `allow`: dial the **name** (not the IP the sandbox picked) through a public-only dialer
     and splice the bytes.
   - `proxy` and `caddy` rules, and any web connection to a host a secret is bound to, are
     intercepted: TLS is terminated with a certificate from the environment's CA, then each
     HTTP request is checked, rewritten and sent on over a new connection to the same host.
     Non-web traffic under such a rule fails.
   - `deny`, a dismissed approval, or no answer in time: HTTP and intercepted TLS get a
     readable 403 (`X-Sandbox-Studio: refused`) telling the agent what happened and whether
     to retry; other protocols are closed.

`DialPublic` refuses non-public addresses, so neither the gateway nor Caddy upstreams reach
the host or private networks.

## Rules

A rule (`store.Rule`) has a host pattern, ports (empty = any port), an action (`allow`,
`proxy`, `caddy`, `deny`), a scope (sandbox, teammate or environment) and, for `proxy`,
headers to set (values may use `{secret.NAME}`); for `caddy`, a Caddyfile snippet.

Matching (`policy/match.go`):

- An environment-wide `deny` always wins, so it works as a fence.
- Otherwise sandbox rules beat teammate rules, which beat environment rules. Teammate rules
  apply only to sandboxes the catalog says that teammate owns.
- Within a scope: the most specific host pattern wins (`*.example.com` also matches
  `example.com`), then rules that name ports, then `deny` over `allow`.

## The Network page

The page (`web/src/routes/e.$env.network*.tsx`) has three tabs, each a route:

- **Rules** lists the environment's rules. Its search and scope and action filters are URL
  search params (`q`, `scope`, `action`), so a filtered view can be shared. Rules are grouped
  by registrable domain: the last two labels, or three under a short list of two-label
  suffixes such as `co.uk` (`web/src/lib/network-rules.ts`). No public suffix list is used.
- **Requests** lists the pending network approvals; Allow, Deny and Ignore work as on the
  approval card.
- **Activity** lists decided network approvals, newest first, up to 50.

`/e/$env/network` opens Requests when any wait for a decision, and Rules otherwise.

## Hold and ask

With no matching rule, the gateway holds the connection (the TLS ClientHello stays
buffered) for up to 60 s and raises a `network` approval. Repeated attempts for the same
subject merge into one pending approval with an attempt counter.

Answers (`policy.Resolve`): allow or deny with a new rule (scope sandbox, teammate or
environment; host pattern from the suggestions: the name or its registrable domain as a
wildcard, never a public suffix), or dismiss. Rules created this way cover 80 and 443 for web
traffic, else the exact port. A new or changed rule settles other pending approvals it
covers and wakes held connections, so an approved `npm install` finishes without a retry.

60 s comes from spike S6: it covers curl, git, npm and apt, and pip succeeds on retry. Node
`fetch` gives up after 10 s, so the UI must show the request quickly.

## Interception and secrets

On intercepted connections (`intercept.go`):

- placeholders are swapped for values in request headers (including Basic credentials),
  path and query, never in bodies (an upstream may store a body and hand it back);
- a placeholder of a secret not bound to this host refuses the request, and so does one of a
  secret the sandbox doesn't see (another sandbox's or teammate's, or one a more specific
  secret of the same name overrides);
- the rule's headers are set;
- the Host header must match the approved host (no domain fronting);
- responses mask the values of the host's secrets (same length, so `Content-Length` holds,
  overlapping values included), and secret-bearing responses drop trailers. This masks
  literal values only; it can't detect an upstream that transforms a credential.

## Caddy rules

A `caddy` rule's config is the body of one site block, compiled by Caddy's Caddyfile adapter
and run as a library (no listeners, no admin endpoint, storage in `<data>/caddy`). The
compiler (`caddyrule/compile.go`) allows a fixed set of matchers, handlers and
`reverse_proxy` options. It refuses environment and file placeholders, `{$VAR}`, `import`
and `encode`; upstreams must be fixed addresses and are dialed with `DialPublic`. Secrets
(`{secret.NAME}`) may appear only in `reverse_proxy` request header values, only for
`https` upstreams the secret is bound to. Caddy never sees them: it gets a marker that
Studio's transport fills in. The sandbox's own placeholders are not swapped under a Caddy
rule.

## Secrets vault

- Values are sealed in `studio.db` with a master key in the OS keychain (macOS Keychain,
  Windows Credential Manager, Secret Service; service "Sandbox Studio", account
  `master-key-<install id>`). Without a keychain the key is a 0600 file `<data>/vault.key`.
  The backend is recorded on first start and only that one is used afterwards; Studio never
  generates a replacement key.
- Each secret has hosts it is bound to: at least one, never a bare `*`.
- Sandboxes see the secret's name as an env var holding a placeholder,
  `studio-` plus 32 base32 characters (160 bits), generated once.
- Like a rule, a secret is scoped to the environment, to one teammate (every sandbox it
  owns) or to one sandbox. The scope is fixed at creation, and a name is unique per scope
  (migration `022_secret_scopes.sql`). A sandbox sees its environment's secrets, its
  teammate's and its own; of secrets sharing a name the most specific wins (sandbox over
  teammate over environment). `Vault.Env` and `Vault.Bindings` resolve this per sandbox, the
  gateway refuses the placeholder of any other secret, and a write reconfigures only the
  sandboxes its scope covers. The browser's `fill_credential` uses the same visibility for
  the sandbox driving it.
- Provider API keys and forge tokens are secrets too, managed by their owner (the secrets
  API answers 409 for them). They always belong to the environment.

## Per-environment CA

Each environment has its own CA (`internal/ca`): 10-year CA, 7-day leaf certificates renewed
a day early, at most 1024 cached leaves per environment. Private keys are stored in the
catalog sealed by the vault. Certificates and placeholders therefore never validate across
environments. How guests trust the CA: [guest agent](guest-agent.md#ca-trust-and-placeholders-in-the-guest).

## Connection log

The gateway keeps the last 500 connections per sandbox, in memory only (`connlog.go`):
host, port, verdict (`pending`, `open`, `allowed`, `denied`, `undecided`, `failed`), rule,
bytes. The sandbox page shows it (`GET …/sandboxes/{id}/connections`).
