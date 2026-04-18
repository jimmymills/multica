package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type AwardEmoji struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	User      User   `json:"user"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (c *Client) ListAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int) ([]AwardEmoji, error) {
	var all []AwardEmoji
	path := fmt.Sprintf("/projects/%d/issues/%d/award_emoji?per_page=100", projectID, issueIID)
	err := iteratePages[AwardEmoji](ctx, c, token, path, func(batch []AwardEmoji) error {
		all = append(all, batch...)
		return nil
	})
	return all, err
}

// CreateIssueAwardEmoji sends POST /api/v4/projects/:id/issues/:iid/award_emoji
// with {"name": "<emoji>"}. Returns the created AwardEmoji (including its ID,
// which the caller stores in the cache as gitlab_award_id so subsequent DELETE
// can target the specific award).
func (c *Client) CreateIssueAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, name string) (*AwardEmoji, error) {
	path := fmt.Sprintf("/projects/%d/issues/%d/award_emoji", projectID, issueIID)
	payload := map[string]any{"name": name}
	var out AwardEmoji
	if err := c.do(ctx, http.MethodPost, token, path, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteIssueAwardEmoji sends DELETE /api/v4/projects/:id/issues/:iid/award_emoji/:award_id.
// Treats GitLab 404 as idempotent success — if the award is already gone,
// that's the desired state.
func (c *Client) DeleteIssueAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, awardID int64) error {
	path := fmt.Sprintf("/projects/%d/issues/%d/award_emoji/%d", projectID, issueIID, awardID)
	err := c.do(ctx, http.MethodDelete, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// CreateNoteAwardEmoji sends POST /api/v4/projects/:id/issues/:iid/notes/:note_id/award_emoji
// with {"name": "<emoji>"}. Returns the created AwardEmoji (including its ID,
// which the caller stores in the cache as gitlab_award_id so subsequent DELETE
// can target the specific award).
func (c *Client) CreateNoteAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, noteID int64, name string) (*AwardEmoji, error) {
	path := fmt.Sprintf("/projects/%d/issues/%d/notes/%d/award_emoji", projectID, issueIID, noteID)
	payload := map[string]any{"name": name}
	var out AwardEmoji
	if err := c.do(ctx, http.MethodPost, token, path, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteNoteAwardEmoji sends DELETE /api/v4/projects/:id/issues/:iid/notes/:note_id/award_emoji/:award_id.
// Treats GitLab 404 as idempotent success — if the award is already gone,
// that's the desired state.
func (c *Client) DeleteNoteAwardEmoji(ctx context.Context, token string, projectID int64, issueIID int, noteID int64, awardID int64) error {
	path := fmt.Sprintf("/projects/%d/issues/%d/notes/%d/award_emoji/%d", projectID, issueIID, noteID, awardID)
	err := c.do(ctx, http.MethodDelete, token, path, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
