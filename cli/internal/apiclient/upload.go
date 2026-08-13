package apiclient

import (
	"bytes"
	"context"
	"mime/multipart"
)

// UploadSourceResponse mirrors uploadSourceResponse in
// backend/internal/api/uploadsource.go.
type UploadSourceResponse struct {
	ArtifactURI string `json:"artifact_uri"`
	Detected    struct {
		Framework string `json:"framework"`
		Port      int    `json:"port"`
	} `json:"detected"`
	Build Build `json:"build"`
}

// UploadSourceArchive posts archiveData (a tar.gz or zip) to the console's
// source-archive endpoint for appName, per
// POST .../apps/:appName/source-archive (backend/internal/api/uploadsource.go).
// The archive size must already have been checked by the caller against
// archive.MaxBytes - this method does not re-check it, since checking after
// building the multipart body would mean discovering the problem only after
// paying the cost of building it.
func (c *Client) UploadSourceArchive(ctx context.Context, projectID, envID, appName, fileName string, archiveData []byte) (*UploadSourceResponse, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	part, err := mw.CreateFormFile("archive", fileName)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(archiveData); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	path := "/projects/" + projectID + "/environments/" + envID + "/apps/" + appName + "/source-archive"
	var out UploadSourceResponse
	if err := c.doJSON(ctx, "POST", path, body, mw.FormDataContentType(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
