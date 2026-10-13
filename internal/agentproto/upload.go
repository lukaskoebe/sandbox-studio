package agentproto

// KindUpload writes a file the host streams into a folder of /workspace. The host writes one
// UploadRequest line and then exactly Size bytes of the file; the guest replies with one
// UploadReply line, or with an Error. The body is read to its end even when the request is
// refused, so the host's writes always complete.
const KindUpload = "upload"

// Upload conflict policies, for a name already taken in the folder.
const (
	ConflictFail    = "fail" // refuse, and write nothing (the default)
	ConflictReplace = "replace"
	ConflictKeep    = "keep" // write the file as the next free "name (2).ext"
)

// MaxUploadBytes bounds one uploaded file.
const MaxUploadBytes = 100 << 20

// CodeExists marks an Error for an upload whose name is taken under ConflictFail.
const CodeExists = "exists"

// UploadRequest is the first line of a KindUpload stream. Dir is relative to /workspace,
// slash-separated; empty is /workspace itself. Name is a single file name.
type UploadRequest struct {
	Dir      string `json:"dir,omitempty"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Conflict string `json:"conflict,omitempty" enum:"fail,replace,keep"`
}

// UploadReply answers an UploadRequest. Name is the name the file was written under, which
// differs from the requested one under ConflictKeep.
type UploadReply struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}
