package api

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
)

// DriveUsage summarizes storage capacity and file count.
type DriveUsage struct {
	UsedQuota       int64 `json:"used_quota"`
	TotalQuota      int64 `json:"total_quota"`
	TotalFileCount  int64 `json:"total_file_count"`
	TotalUsageBytes int64 `json:"total_usage_bytes"`
}

// DriveQuota summarizes the allocation tiers of an account.
type DriveQuota struct {
	BasedQuota    int64 `json:"based_quota"`
	LevelingQuota int64 `json:"leveling_quota"`
	PerkQuota     int64 `json:"perk_quota"`
	ExtraQuota    int64 `json:"extra_quota"`
	TotalQuota    int64 `json:"total_quota"`
}

// DriveList fetches the children of a folder or the root indexed items.
func (c *Client) DriveList(ctx context.Context, parentID string, offset, take int) (Page[CloudFile], error) {
	if take <= 0 {
		take = 50
	}
	var path string
	if parentID == "" {
		path = pagePath("/drive/files/root/children", offset, take)
	} else {
		path = pagePath("/drive/files/"+url.PathEscape(parentID)+"/children", offset, take)
	}

	var items []CloudFile
	meta, err := c.doRootPage(ctx, http.MethodGet, path, nil, &items)
	return Page[CloudFile]{Items: items, Total: totalFrom(meta.Header)}, err
}

// DriveFileInfo fetches the metadata of a single cloud file or folder.
func (c *Client) DriveFileInfo(ctx context.Context, id string) (CloudFile, error) {
	var file CloudFile
	err := c.doRoot(ctx, http.MethodGet, "/drive/files/"+url.PathEscape(id)+"/info", nil, &file)
	return file, err
}

// DriveCreateFolder creates a new folder in Drive.
func (c *Client) DriveCreateFolder(ctx context.Context, name, parentID string) (CloudFile, error) {
	body := map[string]any{"name": name}
	if parentID != "" {
		body["parent_id"] = parentID
	}
	var file CloudFile
	err := c.doRoot(ctx, http.MethodPost, "/drive/files/folders", body, &file)
	return file, err
}

// DriveRename updates a file or folder's display name.
func (c *Client) DriveRename(ctx context.Context, id, name string) (CloudFile, error) {
	var file CloudFile
	err := c.doRoot(ctx, http.MethodPatch, "/drive/files/"+url.PathEscape(id), map[string]string{"name": name}, &file)
	return file, err
}

// DriveRecycle moves a file to the recycle bin.
func (c *Client) DriveRecycle(ctx context.Context, id string) error {
	return c.doRoot(ctx, http.MethodPost, "/drive/files/"+url.PathEscape(id)+"/recycle", nil, nil)
}

// DriveRestore restores a recycled file.
func (c *Client) DriveRestore(ctx context.Context, id string) error {
	return c.doRoot(ctx, http.MethodPost, "/drive/files/"+url.PathEscape(id)+"/restore", nil, nil)
}

// DriveDelete permanently deletes a file or folder.
func (c *Client) DriveDelete(ctx context.Context, id string) error {
	return c.doRoot(ctx, http.MethodDelete, "/drive/files/"+url.PathEscape(id), nil, nil)
}

// DriveDownload downloads the binary content of a file.
func (c *Client) DriveDownload(ctx context.Context, id string) ([]byte, string, error) {
	reqURL := c.rootEndpoint("/drive/files/" + url.PathEscape(id) + "?download=true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", c.UA)
	if token := c.AccessToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download file %s failed: status %d", id, resp.StatusCode)
	}

	name := filenameFromHeader(resp.Header.Get("Content-Disposition"))
	if name == "" {
		name = id
	}
	data, err := io.ReadAll(resp.Body)
	return data, name, err
}

// DriveUsage reports storage usage for the current account.
func (c *Client) DriveUsage(ctx context.Context) (DriveUsage, error) {
	var usage DriveUsage
	err := c.doRoot(ctx, http.MethodGet, "/drive/billing/usage", nil, &usage)
	return usage, err
}

// DriveQuota reports storage quota details for the current account.
func (c *Client) DriveQuota(ctx context.Context) (DriveQuota, error) {
	var quota DriveQuota
	err := c.doRoot(ctx, http.MethodGet, "/drive/billing/quota", nil, &quota)
	return quota, err
}

func filenameFromHeader(disposition string) string {
	if disposition == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		return ""
	}
	return filepath.Base(params["filename"])
}
