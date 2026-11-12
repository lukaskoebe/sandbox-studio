package agentproto

// Turn snapshots and the read-only file browser of chats (docs/design/chat-mode.md
// section 5). Both are request streams like KindHomeFiles: the host writes one request
// line and the guest replies with one line, or an Error.
const (
	// KindSnapshot takes, compares, restores and collects turn snapshots of /workspace.
	KindSnapshot = "snapshot"
	// KindFiles lists a directory or reads a file of /workspace.
	KindFiles = "files"
)

// Snapshot ops.
const (
	// SnapshotTake snapshots /workspace as ID. Files unchanged since Base (same size and
	// mtime) are not reread, and the reply lists what changed since Base.
	SnapshotTake = "take"
	// SnapshotChanges compares snapshot Base with snapshot ID, with diffs.
	SnapshotChanges = "changes"
	// SnapshotRestore puts /workspace back as it was in snapshot ID.
	SnapshotRestore = "restore"
	// SnapshotGC removes every snapshot not in Keep and the blobs only they used.
	SnapshotGC = "gc"
)

// Snapshot limits.
const (
	MaxSnapshotFile   = 16 << 20 // larger files are left out of snapshots and never restored
	MaxSnapshotBytes  = 2 << 30  // the store's cap per sandbox; the oldest snapshots go first
	MaxSnapshotKeep   = 4096     // IDs in one gc request
	MaxChangesListed  = 1000     // changes in one reply; More counts the rest
	MaxDiffBytes      = 128 << 10
	MaxDiffTotalBytes = 512 << 10
	MaxGuestReply     = 2 << 20 // reply lines of KindSnapshot and KindFiles
)

// SnapshotRequest is the body of a KindSnapshot stream. IDs are like session names.
type SnapshotRequest struct {
	Op   string   `json:"op"`
	ID   string   `json:"id,omitempty"`
	Base string   `json:"base,omitempty"`
	Path string   `json:"path,omitempty"` // changes: only this file
	Keep []string `json:"keep,omitempty"`
}

// SnapshotReply answers a SnapshotRequest.
type SnapshotReply struct {
	// Take.
	Files int   `json:"files,omitempty"`
	Bytes int64 `json:"bytes,omitempty"`
	// Dropped are snapshots removed to keep the store under its cap, or the disk from filling.
	Dropped []string `json:"dropped,omitempty"`
	// NoRoom says the disk had too little room to keep the snapshot: it is gone, and the turn
	// can't be undone to it. Partial says the walk stopped when the disk filled, so Changes
	// are unknown.
	NoRoom  bool `json:"noRoom,omitempty"`
	Partial bool `json:"partial,omitempty"`
	// Take and changes.
	Changes []FileChange `json:"changes,omitempty"`
	More    int          `json:"more,omitempty"`
	// Restore.
	Restored int      `json:"restored,omitempty"`
	Removed  int      `json:"removed,omitempty"`
	Failed   []string `json:"failed,omitempty"`
	// Kept counts files the snapshot doesn't have that stayed, because git couldn't say
	// whether they are ignored.
	Kept int `json:"kept,omitempty"`
}

// FileChange is one file a turn changed.
type FileChange struct {
	Path    string `json:"path"`
	Status  string `json:"status" enum:"added,modified,deleted"`
	Added   int    `json:"added" doc:"Lines added"`
	Removed int    `json:"removed" doc:"Lines removed"`
	Binary  bool   `json:"binary,omitempty" doc:"Not text, or too large to compare by line"`
	// Diff is a unified diff, only from SnapshotChanges.
	Diff      string `json:"diff,omitempty" doc:"Unified diff of the file"`
	Truncated bool   `json:"truncated,omitempty" doc:"The diff was cut, or left out to keep the reply small"`
}

// File ops.
const (
	FilesList = "list"
	FilesRead = "read"
	// FilesIndex lists the workspace's files for mentions in a chat: in a git repository the
	// files git knows, tracked or untracked but not ignored; elsewhere every file, past the
	// folders of dependencies and caches.
	FilesIndex = "index"
	// FilesGit lists the git repositories of /workspace as git sees them: each one's branch
	// and what is staged, not staged and untracked in it (FilesReply.Repos).
	FilesGit = "git"
	// FilesGitDiff is one file's diff in one of them: Path is the repository, File the file
	// in it, From its old name when it was renamed, and Area which change of it.
	FilesGitDiff = "git-diff"
)

// The areas of a git change: the index, the working tree, and files git doesn't track.
const (
	GitStaged    = "staged"
	GitUnstaged  = "unstaged"
	GitUntracked = "untracked"
)

// Git limits.
const (
	MaxGitRepos   = 50
	MaxGitChanges = 1000      // of one repository
	MaxGitDiff    = 512 << 10 // bytes of one diff
)

// GitRepo is a repository of /workspace as git status sees it.
type GitRepo struct {
	Path     string `json:"path" doc:"Relative to /workspace; empty for /workspace itself"`
	Branch   string `json:"branch,omitempty" doc:"Empty when HEAD is detached"`
	Head     string `json:"head,omitempty" doc:"The commit checked out; empty before the first"`
	Upstream string `json:"upstream,omitempty" doc:"The branch it tracks, such as origin/main"`
	Ahead    int    `json:"ahead" doc:"Commits it has that its upstream doesn't"`
	Behind   int    `json:"behind" doc:"Commits its upstream has that it doesn't"`
	// Changes are in git status's order: by path, each area apart.
	Changes   []GitChange `json:"changes"`
	Truncated bool        `json:"truncated,omitempty" doc:"More than 1,000 changes"`
	Error     string      `json:"error,omitempty" doc:"Why git couldn't read it"`
}

// GitChange is one file's change in one area of a repository. A file staged and then changed
// again is in both areas.
type GitChange struct {
	Path    string `json:"path" doc:"Relative to the repository"`
	Area    string `json:"area" enum:"staged,unstaged,untracked"`
	Status  string `json:"status" enum:"added,modified,deleted,renamed,conflicted"`
	From    string `json:"from,omitempty" doc:"The old path of a renamed file"`
	Added   int    `json:"added" doc:"Lines added; 0 for an untracked file"`
	Removed int    `json:"removed" doc:"Lines removed"`
	Binary  bool   `json:"binary,omitempty"`
}

// File limits.
const (
	MaxListEntries = 1000
	MaxReadBytes   = 1 << 20
	MaxIndexPaths  = 20000
	MaxIndexBytes  = 1 << 20 // of the paths in all
)

// FilesRequest is the body of a KindFiles stream. Path is relative to /workspace,
// slash-separated; empty is /workspace itself.
type FilesRequest struct {
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`
	// Root is the folder Path is relative to: FilesRootTmp for /tmp, FilesRootFS for /, else
	// /workspace. A guest that knows a root says so in FilesReply.Root.
	Root string `json:"root,omitempty"`
	// File, From and Area name a FilesGitDiff's change.
	File string `json:"file,omitempty"`
	From string `json:"from,omitempty"`
	Area string `json:"area,omitempty"`
}

// FilesRootTmp roots a FilesRequest at /tmp.
const FilesRootTmp = "tmp"

// FilesRootFS roots a FilesRequest at /, the whole computer.
const FilesRootFS = "fs"

// FileEntry is a directory entry.
type FileEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type" enum:"file,dir,symlink,other"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime" doc:"Unix seconds"`
}

// FilesReply answers a FilesRequest.
type FilesReply struct {
	// Root echoes FilesRequest.Root.
	Root string `json:"root,omitempty"`
	// List.
	Entries   []FileEntry `json:"entries,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	// Index: the files' paths, and those git reports changed.
	Paths   []string `json:"paths,omitempty"`
	Changed []string `json:"changed,omitempty"`
	// Git: the repositories, and Truncated past MaxGitRepos. GitDiff: the diff, and Truncated
	// when it was cut at MaxGitDiff.
	Repos []GitRepo `json:"repos,omitempty"`
	Diff  string    `json:"diff,omitempty"`
	// Read.
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     []byte `json:"data,omitempty"`
	// TooLarge, Binary and Unreadable explain a read without Data.
	TooLarge   bool `json:"tooLarge,omitempty"`
	Binary     bool `json:"binary,omitempty"`
	Unreadable bool `json:"unreadable,omitempty"`
}
