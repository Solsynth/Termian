package api

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
)

// DirectUploadLimit is the largest file the single-request Drive endpoint
// accepts. Larger files need the chunked upload task flow.
const DirectUploadLimit = 20 << 20

// UploadFile uploads one file to Drive with the direct single-request endpoint
// and returns its metadata. contentType defaults to application/octet-stream.
func (c *Client) UploadFile(ctx context.Context, name string, data []byte, contentType string) (CloudFile, error) {
	return c.UploadDriveFile(ctx, name, data, contentType, "")
}

// UploadDriveFile uploads one file to Drive with an optional parent folder ID.
func (c *Client) UploadDriveFile(ctx context.Context, name string, data []byte, contentType, parentID string) (CloudFile, error) {
	if len(data) > DirectUploadLimit {
		return CloudFile{}, fmt.Errorf("file %q is larger than the %d MiB direct upload limit", filepath.Base(name), DirectUploadLimit>>20)
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filepath.Base(name))
	if err != nil {
		return CloudFile{}, err
	}
	if _, err := part.Write(data); err != nil {
		return CloudFile{}, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := writer.WriteField("content_type", contentType); err != nil {
		return CloudFile{}, err
	}
	if parentID != "" {
		if err := writer.WriteField("parent_id", parentID); err != nil {
			return CloudFile{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return CloudFile{}, err
	}

	var file CloudFile
	_, err = c.doAt(ctx, c.rootEndpoint("/drive/files/upload/direct"), http.MethodPost,
		"/drive/files/upload/direct", writer.FormDataContentType(), &buf, &file)
	return file, err
}
