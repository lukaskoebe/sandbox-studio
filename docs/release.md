# Cutting a release

Releases are built by [`.github/workflows/release.yml`](../.github/workflows/release.yml) when a
version tag is pushed:

```sh
git switch main && git pull
git tag -a v0.1.0 -m v0.1.0     # a tag with a hyphen (v0.1.0-rc.1) becomes a pre-release
git push origin v0.1.0
```

The workflow then:

1. Builds `images/base` for linux/amd64 and linux/arm64. It pushes the image to
   `ghcr.io/lukaskoebe/sandbox-studio-base:<tag>` and attests it. The resulting
   `…@sha256:` reference is the image that release uses.
2. Builds the web UI and both Linux guest agents once.
3. Builds `studio` on a native runner for each target: linux/amd64, linux/arm64,
   darwin/arm64 and windows/amd64. The microsandbox SDK needs CGO, so nothing is
   cross-compiled. The build embeds the UI and the agents. Its ldflags set
   `internal/version.Version` to the tag and `internal/version.BaseImage` to the image digest.
4. Signs and notarizes the macOS binary, and signs the Windows binary, when the secrets below
   exist. Without them those steps are skipped and the binaries are unsigned.
5. Packages `.tar.gz` (Linux and macOS) and `.zip` (Windows) archives. It writes
   `checksums.txt`, adds build provenance attestations, and creates the GitHub release with
   generated notes.

Locally, `make build BASE_IMAGE=ghcr.io/…@sha256:…` sets the same ldflag.

## First release

- The ghcr package is created on the first push and starts private. Make it public under the
  package's settings (Package settings → Change visibility); otherwise users cannot pull the
  base image.
- Artifact attestations on a public repository need no setup.

## Secrets

Set these under Settings → Secrets and variables → Actions. Each group is optional.

**macOS signing and notarization** need a "Developer ID Application" certificate from a paid
Apple Developer account:

| Secret | Value |
|---|---|
| `APPLE_CERTIFICATE_P12` | The certificate and private key exported as `.p12`, base64-encoded (`base64 -i cert.p12`) |
| `APPLE_CERTIFICATE_PASSWORD` | The `.p12` export password |
| `APPLE_SIGNING_IDENTITY` | e.g. `Developer ID Application: Your Name (TEAMID)` |
| `APPLE_ID` | The Apple ID used for notarization |
| `APPLE_TEAM_ID` | The 10-character team ID |
| `APPLE_APP_PASSWORD` | An app-specific password for that Apple ID (appleid.apple.com) |

The binary is signed with the hardened runtime and
[`packaging/macos/studio.entitlements`](../packaging/macos/studio.entitlements). That file turns
off library validation, because Studio loads the microsandbox and llama.cpp libraries at
runtime. A bare executable cannot be stapled, so Gatekeeper checks the notarization ticket
online on first launch.

**Windows signing** needs a code-signing certificate as a `.pfx`:

| Secret | Value |
|---|---|
| `WINDOWS_CERT_PFX` | The `.pfx`, base64-encoded |
| `WINDOWS_CERT_PASSWORD` | Its password |

A certificate whose key must stay on a hardware token or in a cloud HSM (most certificates
issued since 2023) cannot be exported as a `.pfx`. In that case, replace the signing step with
the vendor's signing action, such as Azure Trusted Signing.

## Updating pinned actions

Actions are pinned by full commit SHA, with the version in a comment. To update one, look up
the SHA that the new version's tag points to:

```sh
gh api repos/actions/checkout/commits/v7.0.1 --jq .sha
```

Then run `actionlint` on the workflow.
