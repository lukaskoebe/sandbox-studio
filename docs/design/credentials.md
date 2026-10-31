# Credentials: short-lived credentials through tools' own helpers

Status: the core and AWS are built (plan steps 1 and 2) except the gateway guard of issued
values and the virtual host; the other kinds follow as they are needed. What users see is in
[network](../network.md#credentials).

## Why

A secret's placeholder is swapped for its value on the way out (docs/network.md), which works
when the value travels as it is: bearer tokens, API keys, Basic credentials. It can't work
when the client derives something from the value or checks it first:

- **It signs with it.** AWS SigV4 and every S3-compatible store, Azure SharedKey, OAuth 1.0a,
  HMAC APIs, SCRAM. The request carries a signature made with the placeholder, which the
  host refuses (`SignatureDoesNotMatch`).
- **It holds a private key.** SSH, GPG, client certificates, JWT service accounts.
- **It checks the value or the CA first.** Format checks fail on a placeholder; clients with
  CA bundles of their own (`AWS_CA_BUNDLE`, Java) don't trust Studio's.

Signing again in the gateway works for one scheme at a time. What generalizes is the other way
of keeping a long-lived secret out of a sandbox: **Studio keeps it and issues the sandbox a
short-lived credential made from it**, which the tool uses unchanged, through the helper
interface it already has for that: AWS `credential_process` and its container endpoint,
GCP's metadata server, kubectl exec plugins, Docker credential helpers, `SSH_AUTH_SOCK`.

Substitution stays the default: it is the stronger guarantee (the value never enters the
sandbox), and it suits static tokens, which nothing can issue anyway. A secret's **kind**
picks the mechanism.

## The parts

| Part | What it is | Changes without a rebuild |
|---|---|---|
| Issuers | code that turns a long-lived secret into a short-lived credential | no: a small built-in set |
| Recipes | data: which issuer, the secret's fields, its hosts, the env vars, files and helper answers | yes |
| Delivery | `studio-agent credential`, a virtual host, managed file blocks, env vars | generic |
| Guard | issued values join the gateway's bindings: masked in responses, refused elsewhere | generic |

### Issuers (code, built in)

A handful of standards cover nearly everything:

- `aws-sts`: `GetSessionToken`, or `AssumeRole` with an optional session policy. AWS and
  every S3-compatible store whose keys are AWS's.
- `oauth2-client-credentials`, `oauth2-refresh-token`.
- `jwt-bearer`: signs a JWT with a key of the vault's and exchanges it (GCP service
  accounts, GitHub Apps, Azure AD, Salesforce).
- `ssh-agent`: no credential is issued; Studio signs with the vault's key.
- Later, `vault`: HashiCorp Vault or OpenBao, whose secrets engines then cover the long tail.

```go
type Issuer interface {
    Issue(ctx context.Context, in IssueInput) (Issued, error)
}
type IssueInput struct {
    Fields   map[string]string // the secret's fields, the user entered
    Secret   string            // the secret field's value
    Lifetime time.Duration
    Session  string            // names the sandbox where the service records sessions
    Client   *http.Client
}
type Issued struct {
    Fields  map[string]string // accessKeyId, secretAccessKey, sessionToken, token, …
    Expires time.Time
}
```

`aws-sts` signs its own requests (SigV4, checked against the AWS CLI's), so Studio needs no
AWS SDK.

Code that sees long-lived secrets stays in Studio's binary. Extensions with code (host
processes, Caddy modules) are out: they would sit next to the vault with Studio's
privileges, and Caddy modules need a rebuild anyway. A sandboxed WASM issuer is the option
if a real case outgrows the standards.

### Recipes (data)

Each kind is a recipe. The built-in ones are embedded in the binary; the user may add their
own in Settings once the format has settled. A recipe never sees the long-lived secret, only
the fields of what was issued; its templates run no code; its files go under the home
directory or `/etc/studio` and its endpoints under `studio.internal`. Agents can't add or
change recipes.

```yaml
name: aws
title: AWS access key
issuer: aws-sts
fields:                     # what the user enters; secret ones go to the vault
  - {name: accessKeyId, title: Access key ID}
  - {name: secretAccessKey, title: Secret access key, secret: true}
  - {name: roleArn, title: Role to assume, optional: true}
hosts: ["*.amazonaws.com"]
lifetime: 1h
env:                        # only in a sandbox that sees one secret of the kind
  AWS_PROFILE: "{{secret | lower}}"
files:                      # a managed block between markers, one entry per secret
  - path: .aws/config       # relative to the home
    comment: "# "           # before the markers, for files that aren't HTML
    content: |
      [profile {{secret | lower}}]
      credential_process = /opt/studio/bin/studio-agent credential {{secret}} process
answers:                    # by format, for the helper command and the endpoint
  process:
    Version: 1
    AccessKeyId: "{{cred.accessKeyId}}"
    SecretAccessKey: "{{cred.secretAccessKey}}"
    SessionToken: "{{cred.sessionToken}}"
    Expiration: "{{cred.expires}}"
```

### Delivery

| Way in | How Studio knows the sandbox | For |
|---|---|---|
| `studio-agent credential <secret> <format>`, a call over the agent channel | the VM's channel | `credential_process`, Docker `credsStore`, kubectl `exec`, git |
| `https://credentials.studio.internal/<secret>/<format>`, a virtual host | the gateway's SOCKS login | AWS container endpoint, and Docker containers in the sandbox, which can't run `studio-agent` |
| a Unix socket of `studio-agent`'s | the VM's channel | `SSH_AUTH_SOCK` |

The command is the default: no network, TLS or CA trust in the way. Env vars are preferred
to files where a tool reads them: the AWS container endpoint is last in AWS's chain, so the
user's own `~/.aws` still wins; `KUBECONFIG` takes a list, so the user's file stays as it
is. File blocks are for what only a file can say, such as AWS's named profiles.

Env vars reach new terminals and sessions; files are written while the VM runs; endpoints
and answers change at once.

### Issuing

- **Which secrets.** A secret of a recipe's kind that the sandbox may see, by the scopes it
  has today (environment, teammate, sandbox).
- **Approval.** The first request of a sandbox for a secret asks (`credential.issue`): once,
  for the sandbox until it stops, or always for the teammate. The security level applies as
  for network requests.
- **Cache.** One issued credential per sandbox and secret, until a few minutes before it
  expires, so a tool that asks its helper on every command doesn't issue every time.
- **Guard.** Issued values join the sandbox's bindings in the gateway, bound to the recipe's
  hosts: masked in responses, refused on other hosts, masked in recordings.
- **Log.** What was issued to which sandbox, and until when, under the secret.
- **Ending.** Nothing is issued once the sandbox stops; the lifetime does the rest. AWS's
  shortest is 15 minutes, and its SDKs refresh by themselves.

## Plan

1. **Core.** Done: secret kinds and fields (migration 057), the issuer and recipe types, the
   cache, the `credential.issue` approval, the `studio-agent credential` command and its
   call, and detection (a SigV4 request naming a placeholder key says what to do instead).
   Still to do: the gateway guard of issued values and the virtual host, with the AWS
   container endpoint (`aws` answers) that Docker containers in a sandbox need.
2. **AWS.** Done: `aws-sts`, the recipe, `AWS_CA_BUNDLE` in every sandbox and container, and
   the secret form's **Kind** select with the recipe's fields.
3. **SSH.** The agent socket: Studio lists the public keys and signs with the vault's key.
4. **GCP.** `jwt-bearer` and the metadata server (`GCE_METADATA_HOST`), which needs plain
   HTTP on that one virtual host, with GCP's `Metadata-Flavor` header.
5. **Docker, kubectl, Azure, Vault**, when they are needed. User recipes in Settings once
   the format has settled with the built-in ones.

Also, independent of kinds: substitution in form and JSON bodies, and placeholders that keep
a value's prefix, length and alphabet.
