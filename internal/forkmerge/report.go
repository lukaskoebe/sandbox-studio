package forkmerge

// Outcomes of a file.
const (
	OutcomeTheirs   = "theirs"   // the fork's version was taken
	OutcomeDeleted  = "deleted"  // the fork deleted it, and so does the merge
	OutcomeMerged   = "merged"   // both changed it and the changes merged cleanly
	OutcomeKept     = "kept"     // a session file both changed: this sandbox's version stays
	OutcomeConflict = "conflict" // see Conflict
)

// Conflicts.
const (
	ConflictText   = "text"   // conflict markers in the file
	ConflictBinary = "binary" // both changed a file that can't be merged line by line
	ConflictType   = "type"   // a file on one side, a symlink on the other
	ConflictDelete = "delete" // one side deleted what the other changed
	ConflictError  = "error"  // the fork's version couldn't be written
)

// Report says what a merge did.
type Report struct {
	Fork      string         `json:"fork"`
	NoBase    bool           `json:"noBase,omitempty"`
	Unchanged int            `json:"unchanged"` // changed paths both sides already agree on
	Files     []FileResult   `json:"files"`
	Branches  []BranchResult `json:"branches"`
	Installs  []string       `json:"installs"`  // tool installs that were copied
	Conflicts int            `json:"conflicts"` // unresolved
}

// FileResult is a path the merge changed or couldn't.
type FileResult struct {
	Path     string `json:"path"`
	Outcome  string `json:"outcome"`
	Conflict string `json:"conflict,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Artifact is the file under the staging directory whose deletion resolves a conflict
	// other than a text one.
	Artifact string `json:"artifact,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

// BranchResult is a branch of the fork, as the merge added it.
type BranchResult struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`        // the fork's
	Ref    string `json:"ref,omitempty"` // where it is now, as refs/heads/...
	Error  string `json:"error,omitempty"`
}

// ReplayResult is what merge-replay answers: what changed in the sandbox since the merge
// started, which the replay refused to overwrite. None means it was applied.
type ReplayResult struct {
	Conflicts []string `json:"conflicts"`
}
