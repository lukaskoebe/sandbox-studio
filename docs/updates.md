# Updates

`internal/updates` checks GitHub for a newer release and, if the user chose so, installs it.
The setting is under **Settings → General → Updates** (`PUT /api/system/updates`):

| Mode | What Studio does |
|---|---|
| `off` | nothing |
| `tell` (the default) | asks GitHub's API for the latest release at most once a day and shows a newer one in Settings and the sidebar |
| `install` | also installs it, as below |

A row saved before the modes keeps its choice: `{"enabled":false}` is off, `{"enabled":true}`
tells. Development builds (`git describe` versions, `-dirty`) never check, since no release
is newer than them for `updates.Newer`, and can't choose `install` (`canInstall` is false), nor
can a Studio that can't find its own binary.

## Installing

`Installer` (`install.go`) runs in the background once a check finds a newer release:

1. **Download.** Only a release that isn't a pre-release and is newer than the running one.
   The archive for this OS and architecture (`sandbox-studio_<tag>_<os>_<arch>.tar.gz`, `.zip`
   on Windows) and `checksums.txt` must be assets of that release on
   `github.com/<repo>/releases/download/<tag>/`. They go to `<data>/updates/<tag>/`.
2. **Verify.** The archive must match its line in `checksums.txt`, and GitHub must have a build
   provenance attestation for its digest (`verify.go`): a Sigstore bundle whose certificate
   GitHub Actions issued to `.github/workflows/release.yml@refs/tags/<tag>` of this repository,
   with a signed certificate timestamp and a Rekor entry, checked against Sigstore's
   public-good trusted root (fetched with TUF, cached in `<data>/updates/tuf`). The binary is
   then unpacked next to the running one (the directory must be writable) and must answer
   `studio version` with the tag.
3. **Wait for idle.** No chat turn, template build or sign-in may be running; Studio checks
   again every two minutes and the UI says what it waits for.
4. **Back up** the database with `VACUUM INTO` to `<data>/updates/<tag>/studio-<old>.db`.
5. **Swap.** The running binary moves to `studio.old` (`studio.old.exe`) and the new one takes
   its name. Renaming first works on Windows too, where a running executable can't be
   replaced. The install record `<data>/updates/install.json` (`Pending`) is written.
6. **Restart.** Studio shuts down as on Ctrl-C. Under systemd it exits with status 75 and the
   unit (`Restart=on-failure`, see `internal/autostart`) starts the new binary. Studio is under
   systemd when `INVOCATION_ID` is set and Studio is the process systemd started:
   `SYSTEMD_EXEC_PID` is its PID, or, before systemd 248, systemd is its parent. A shell in a
   terminal that a systemd service started inherits `INVOCATION_ID`, and nothing would start
   Studio again there. Otherwise the old process starts the new binary detached, with
   `-no-browser` (`Setsid` on Linux and macOS, a detached process outside the job on Windows),
   and waits; once the new one serves, the old one exits and logs the new one's PID. The
   terminal gets its prompt back, and `studio quit` stops the Studio that runs on.

## Health and rollback

The new binary reads the record before it opens the database (`ResumeInstall`) and counts
its start. Once its own `/api/auth/status` answers, it marks the record installed, removes the old
binary and the downloads of earlier installs; the backup of this install stays.

It is rolled back, meaning the old binary moves back, the new one stays as `studio.failed`,
the backup replaces the database and its WAL is dropped, when:

- it exits before serving, or doesn't serve within two minutes, while the old process watches
  it (`RestartUnsupervised`); the old process then starts the old binary;
- under systemd, it hasn't served two minutes after starting (`Watchdog`), or starts a fourth
  time without having served; it exits with 75 and systemd starts the old binary;
- the old binary starts while the record still says the new one hasn't served.

`store.Open` refuses a database with a newer schema than the binary knows, which is why a
rollback restores the backup instead of running the old binary on the migrated database.
The outcome, installed or rolled back with the reason, shows under Updates.
