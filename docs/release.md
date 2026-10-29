# Cutting a release

Releases are built by [`.github/workflows/release.yml`](../.github/workflows/release.yml).
A release is cut from a release branch, and its version tag is pushed only after everything
has built and passed on that branch:

1. Branch `release/v0.6.0` from main. On the branch, give the version's section in
   [CHANGELOG.md](../CHANGELOG.md) its date, commit, and push the branch:

   ```sh
   git switch main && git pull
   git switch -c release/v0.6.0
   # rename "## Unreleased" to "## v0.6.0 — YYYY-MM-DD" and commit it
   git push -u origin release/v0.6.0
   ```

2. The push runs ci and the release workflow's dry run. The dry run is every step below
   except publishing: it builds the images (pushed as `<version>-candidate`), the UI, the
   agents and all four binaries, and runs the smoke tests. Wait for both to pass on the
   branch's head:

   ```sh
   gh run list --branch release/v0.6.0
   gh run watch <id> --exit-status
   ```

   A fix goes onto the branch (and into main); push it and wait again. A flake is re-run with
   `gh run rerun <id> --failed`.

3. Tag the head that passed and push the tag, which builds again and publishes:

   ```sh
   git tag -a v0.6.0 -m v0.6.0 release/v0.6.0   # a tag with a hyphen (v0.6.0-rc.1) becomes a pre-release
   git push origin v0.6.0
   ```

4. Merge the branch back into main, so main has the dated changelog and any fixes, and delete
   it:

   ```sh
   git switch main && git pull && git merge release/v0.6.0 && git push
   git push origin --delete release/v0.6.0
   ```

The branch's name gives the version (`release/v0.6.0-rc.1` for a pre-release); the workflow
refuses one that doesn't name a version. A patch release branches from the release it fixes,
e.g. `git switch -c release/v0.6.1 v0.6.0`, with the fixes cherry-picked from main.

Each run of the workflow:

1. Builds `images/base` and `images/browser` for linux/amd64 and linux/arm64. It pushes them
   to `ghcr.io/lukaskoebe/sandbox-studio-base:<tag>` and
   `ghcr.io/lukaskoebe/sandbox-studio-browser:<tag>` (`<version>-candidate` from a release
   branch) and attests both. The resulting
   `…@sha256:` references are the images that release uses.
2. Builds the web UI and both Linux guest agents once.
3. Builds `studio` on a native runner for each target: linux/amd64, linux/arm64,
   darwin/arm64 and windows/amd64. The microsandbox SDK needs CGO, so nothing is
   cross-compiled. The build embeds the UI and the agents. Its ldflags set
   `internal/version.Version` to the version, and `internal/version.BaseImage` and
   `internal/version.BrowserImage` to the image digests.
4. Signs and notarizes the macOS binary, and signs the Windows binary, when the secrets below
   exist. Without them those steps are skipped and the binaries are unsigned.
5. Runs [`smoke.yml`](../.github/workflows/smoke.yml) with the pushed base image: on Linux,
   macOS and Windows runners, Studio built from the tag or branch creates a sandbox from that
   image and waits for its guest agent, Linux and Windows run the live browser tests
   (`web/e2e-live`), and every host embeds with the real local model. The macOS runners have
   no Hypervisor.framework, so they skip the boot with a warning. Nothing
   is published unless this passes; if it fails on a flake, re-run the failed jobs.
6. Packages `.tar.gz` (Linux and macOS) and `.zip` (Windows) archives named
   `sandbox-studio_<tag>_<os>_<arch>`, each holding a directory of the same name with
   `studio`, `studio-app` (the [Studio app](app.md); on macOS `Sandbox Studio.app`, signed,
   notarized and stapled), `LICENSE` and `README.md`, and on Windows also `studiow.exe`, the
   tray launcher. It writes `checksums.txt`, adds build provenance
   attestations, and creates the GitHub release with generated notes. Only a tag's run does
   this; a release branch's run keeps the archives as workflow artifacts.

[`install.sh`](../install.sh) depends on those names: it downloads
`sandbox-studio_<tag>_<os>_<arch>.tar.gz` and `checksums.txt` from the release, so keep them
in step when changing the workflow. It installs the newest non-prerelease, or the tag in
`SANDBOX_STUDIO_VERSION`. `sh scripts/test-install.sh` tests it offline against a fake
release.

Locally, `make build BASE_IMAGE=ghcr.io/…@sha256:…` sets the same ldflag.

Once a tag's run succeeds, [`screenshots.yml`](../.github/workflows/screenshots.yml) takes the
docs screenshots with that release's Linux binary on a KVM runner (`site/scripts/screenshots.mjs`,
with main's script) and opens a pull request `screenshots/<tag>` with the images whose pixels
changed (`site/scripts/screenshots-changed.py`: more than 0.2% of the pixels). Run it by hand
for any release from the Actions tab. It needs "Allow GitHub Actions to create and approve
pull requests" under Settings → Actions → General. Checks don't start on a pull request the
workflow opened; push to it, or close and reopen it, to run them.

## First release

- The ghcr packages are created on the first push and start private. Make both
  (`sandbox-studio-base` and `sandbox-studio-browser`) public under each package's settings
  (Package settings → Change visibility); otherwise users cannot pull the images.
- Artifact attestations on a public repository need no setup.
- Turn on private vulnerability reporting (Settings → Code security → Private vulnerability
  reporting); [SECURITY.md](../SECURITY.md) points reporters to it.
- After the release, install it with `install.sh` on Linux and on a Mac, run
  `studio version` and `studio doctor`, and create a sandbox, which pulls the base image
  anonymously from ghcr.io.

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
