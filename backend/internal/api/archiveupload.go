package api

import (
	"path"
	"regexp"
	"strings"
)

// archiveUploadIDMaxLen is the number of characters of the archive object
// key's basename (extension stripped) kept as the upload id. Matches
// build-agent's archiveUploadBranch, which truncates the same id to 8 chars
// before prefixing it with "upload-" to form a branch name; here the raw id
// is used directly as a build's head_sha, so the two stay in sync.
const archiveUploadIDMaxLen = 8

// archiveUploadIDFromCloneURL derives a stable, human-identifiable upload id
// from a git_repos.clone_url of the form "s3://<bucket>/<key>", where key
// ends in an upload UUID plus archive extension (see UploadSourceArchive).
// It mirrors build-agent/internal/worker/archivesource.go's archiveUploadID:
// take the object key's basename, strip a known archive extension (falling
// back to path.Ext for anything else), and keep the first 8 characters.
//
// A malformed or non-s3 URL, or one with no basename left after stripping,
// yields "" — the caller must leave head_sha NULL rather than invent a
// value.
func archiveUploadIDFromCloneURL(cloneURL string) string {
	const prefix = "s3://"
	if !strings.HasPrefix(cloneURL, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(cloneURL, prefix)
	bucket, key, ok := strings.Cut(rest, "/")
	if !ok || bucket == "" || key == "" {
		return ""
	}

	base := path.Base(key)
	id := base
	stripped := false
	for _, ext := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(base, ext) {
			id = strings.TrimSuffix(base, ext)
			stripped = true
			break
		}
	}
	if !stripped {
		id = strings.TrimSuffix(base, path.Ext(base))
	}
	if id == "" {
		return ""
	}
	if len(id) > archiveUploadIDMaxLen {
		id = id[:archiveUploadIDMaxLen]
	}
	return id
}

// uploadFilenameMaxLen caps the sanitized filename length stored as a
// build's commit_message. Long enough for any real filename, short enough
// to never stress the column or the console's rendering.
const uploadFilenameMaxLen = 255

var uploadFilenameControlCharsRe = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// sanitizeUploadedFilename prepares a user-supplied multipart filename
// (multipart.FileHeader.Filename) for storage and console display: it drops
// any path components a client might have sent (forward or back slash),
// strips control characters, trims surrounding whitespace, and caps the
// length. The result is untrusted user input rendered verbatim in the
// console, so nothing here treats it as a real filesystem path.
func sanitizeUploadedFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" {
		return ""
	}
	name = uploadFilenameControlCharsRe.ReplaceAllString(name, "")
	name = strings.TrimSpace(name)
	if len(name) > uploadFilenameMaxLen {
		name = name[:uploadFilenameMaxLen]
	}
	return name
}
