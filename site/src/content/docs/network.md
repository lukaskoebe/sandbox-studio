---
title: Network rules and approvals
description: Decide what sandbox traffic can reach, and what happens when a host is unknown.
---

Every sandbox TCP connection goes through Studio's gateway, including connections made by Docker containers inside the sandbox. DNS uses Studio's resolver. UDP and ICMP are blocked; outbound connections are for public addresses.

## Unknown hosts wait for you

When a sandbox reaches a host not covered by a rule, Studio holds the connection and shows a request in the approval inbox. The request includes the host, port, sandbox, and teammate if there is one.

You can allow or deny the request, or add a rule with a wider scope. Studio holds the original connection for up to 60 seconds. If you allow it before the hold expires and the client is still waiting, Studio releases that same connection and the original operation continues. If the client gives up or the 60-second hold expires first, the connection closes; retry the operation after approval.

![A network request waiting for an Allow or Deny decision](../../assets/screenshots/approval.jpg)

The Network page has three tabs. **Rules** lists your rules, grouped by domain, with search and filters for scope and action. **Requests** lists the connections waiting for your decision, and **Activity** lists the ones you already decided. To see which hosts one sandbox reached, open its **Connections** panel.

## Add a rule

On the Network page's **Rules** tab, choose **Add rule**. Enter a host such as `api.example.com` or `*.example.com`; a wildcard covers the root host and its subdomains. Add comma-separated ports, or leave ports empty to match every port. Choose whether the rule applies to the whole environment, one teammate, or one sandbox.

![The Add rule dialog with host, port, action, and scope fields](../../assets/screenshots/rule-dialog.jpg)

| Action | Use it for |
| --- | --- |
| **Allow** | Let matching connections through without a custom header rule. |
| **Proxy** | Let Studio handle HTTP and HTTPS requests and set request headers, such as an authorization header. |
| **Caddy** | Apply a restricted Caddyfile rule to rewrite, answer, or proxy HTTP requests. |
| **Deny** | Block matching connections. An environment-wide deny always wins. |

Environment-level deny rules take precedence as a safety boundary. Otherwise a sandbox-specific rule, then a teammate rule, then other environment rules are considered.

Proxy and Caddy rules handle HTTP and HTTPS requests inside Studio. Studio installs a per-environment certificate authority in each sandbox so it can intercept TLS for these rules. A host-bound secret also uses this path when Studio needs to replace its placeholder.

## Use a secret in a proxy rule

For a **Proxy** rule, enter a header value with a secret placeholder, for example `{secret.API_TOKEN}`. Studio replaces it only for HTTPS requests to a host that the secret is bound to. See [Secrets](../secrets/).

For a **Caddy** rule, the field accepts the body of a Caddy site block, not a full server configuration. This example proxies requests to an HTTPS API and adds a bound secret as a header:

```text title="Caddyfile"
reverse_proxy https://api.example.com {
  header_up X-Api-Key {secret.API_TOKEN}
}
```

Create the secret first and bind it to `api.example.com`. Studio checks Caddy rules before saving them. Rules support a limited set of request handlers and fixed public upstream hosts; imports, file access, environment lookups, extra listeners, and arbitrary Caddy modules are not available.

## Browser and Git approvals are separate

The network policy controls which hosts a sandbox can reach. Browser actions that change a page can also need a **browser action** approval. Git pushes to a forge configured in the Git page use **Git push** review. A network allow does not approve either of those actions. See [Browser](../browser/) and [Git review](../git-review/).
