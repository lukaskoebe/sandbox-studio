// Package forkmerge merges a fork's changes back into the sandbox it was forked from
// (docs/design/fork-merge.md). It runs in the guest, as the agent user: `studio-agent
// merge-*` records bases, writes archives of changes and applies them.
package forkmerge
