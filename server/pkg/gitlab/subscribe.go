package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Subscribe sends POST /api/v4/projects/:id/issues/:iid/subscribe. GitLab
// returns 304 Not Modified when the user (identified by the PAT) is already
// subscribed — treated as idempotent success.
func (c *Client) Subscribe(ctx context.Context, token string, projectID int64, issueIID int) error {
	path := fmt.Sprintf("/projects/%d/issues/%d/subscribe", projectID, issueIID)
	err := c.do(ctx, http.MethodPost, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotModified) {
		return nil
	}
	return err
}

// Unsubscribe sends POST /api/v4/projects/:id/issues/:iid/unsubscribe. GitLab
// returns 304 Not Modified when the user is not currently subscribed —
// treated as idempotent success.
func (c *Client) Unsubscribe(ctx context.Context, token string, projectID int64, issueIID int) error {
	path := fmt.Sprintf("/projects/%d/issues/%d/unsubscribe", projectID, issueIID)
	err := c.do(ctx, http.MethodPost, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotModified) {
		return nil
	}
	return err
}
