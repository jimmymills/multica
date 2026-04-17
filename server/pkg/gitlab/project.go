package gitlab

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Project mirrors the subset of GET /api/v4/projects/:id we care about.
type Project struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
	Description       string `json:"description"`
}

// GetProject looks up a project by numeric ID or by URL-encoded path ("group/project").
func (c *Client) GetProject(ctx context.Context, token, idOrPath string) (*Project, error) {
	// Numeric → use as-is; path → URL-encode slashes (GitLab convention).
	ref := idOrPath
	if _, err := strconv.ParseInt(idOrPath, 10, 64); err != nil {
		ref = url.PathEscape(strings.TrimPrefix(idOrPath, "/"))
	}
	var p Project
	if err := c.get(ctx, token, "/projects/"+ref, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
