# Reports from agents

An agent that finds something in Studio or its sandbox confusing, misleading or broken (an
instruction that doesn't hold, a tool that answers wrongly, an error that names the wrong
cause) can offer the user a report of it for Studio's developers. The user decides whether it
is sent; nothing leaves the machine unless they send it. Package `internal/reports`.

## The flow

1. The agent calls `report_issue` (MCP) or `studio report <title> --body-file -`, with a title
   and a body: what it did, what happened, what it expected. The agent instructions mention
   the command in one line (`machineInstructions` in `internal/harness`).
2. Studio takes out what could tell who sent it (below), adds a footer with the versions it ran
   on, and raises a `studio.report` approval. The agent hears back `"status": "offered"` with
   how many things were taken out, and doesn't wait: the command exits 0.
3. The card shows the title and text, editable, and how many things Studio took out.
   **Open on GitHub…** opens GitHub's new-issue page of Sandbox Studio's repository
   (`updates.Repository`) with the title and text filled in, in the user's browser (the app
   hands `_blank` links to the system browser), and settles the card as approved. The user
   sends the issue there, under their own account, or closes the tab. **Don't send** dismisses
   the card.

The issue is public and filed by the user, so they see exactly what is sent, and GitHub's own
form is the last step. Studio runs no service to receive reports and keeps no GitHub token.

## What Studio takes out

`reports.Anonymize`, with the names `Service.names` collects:

- The names Studio knows: the environment's, its computers', teammates' names and git
  identities, forges with their user names and hosts, the local network list, the repositories,
  owners and branches the sandbox pushed; the user Studio runs as, the words of their full
  name, the machine's name, and the host's git identity.
- Email addresses, IP addresses other than loopback, the user part of home folders
  (`/home/<user>`, `/Users/<user>`, `C:\Users\<user>`, except the guest's `agent`), and project
  names under `/workspace/<project>`.
- URLs keep their host only for public services (the big forges, the package registries, the
  AI providers) and Studio's own `*.studio.internal`, and their path only for registries and
  Studio's hosts; others read `https://<host>/<path>`. A path on `git.studio.internal` reads
  `/<forge>/<repository>`. SSH remotes read `git@<host>:<repository>`.
- Secrets: the key shapes of the common providers (`sk-`, `ghp_`, `github_pat_`, `glpat-`,
  `xox`, `AKIA`), JWTs, and long runs that mix upper case, lower case and digits. Commit
  hashes stay.

Names shorter than three characters are left alone, and each replacement is a `<label>` so the
text still reads. It is a best effort: the card says how much it took out and asks the user to
read the report before sending it.

## Limits

At most three reports wait in an environment at once; another is refused with a word to the
agent to mention it in its message instead. A report with the same title as one waiting
replaces it. GitHub's new-issue page takes its text from the address, so a report whose link
would pass 7,500 characters asks the user to shorten it.
