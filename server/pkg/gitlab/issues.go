package gitlab

import (
	"context"
	"fmt"
	"net/url"
)

// CreateIssueInput is the body for POST /projects/:id/issues. Only fields
// we set are listed; GitLab accepts more.
type CreateIssueInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	// AssigneeIDs is the list of GitLab user IDs to assign. Empty when
	// Multica is assigning to an agent (we use the agent::<slug> label
	// instead).
	AssigneeIDs []int64 `json:"assignee_ids,omitempty"`
	DueDate     string  `json:"due_date,omitempty"`
}

// CreateIssue creates a new issue in the project and returns the GitLab
// representation (which the caller can run through the translator + cache
// upsert).
func (c *Client) CreateIssue(ctx context.Context, token string, projectID int64, input CreateIssueInput) (*Issue, error) {
	var out Issue
	path := fmt.Sprintf("/projects/%d/issues", projectID)
	if err := c.do(ctx, "POST", token, path, input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Issue struct {
	ID          int64    `json:"id"`
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	Assignees   []User   `json:"assignees"`
	Author      User     `json:"author"`
	DueDate     string   `json:"due_date"`
	UpdatedAt   string   `json:"updated_at"`
	CreatedAt   string   `json:"created_at"`
	WebURL      string   `json:"web_url"`
}

type ListIssuesParams struct {
	State        string
	UpdatedAfter string
}

func (c *Client) ListIssues(ctx context.Context, token string, projectID int64, params ListIssuesParams) ([]Issue, error) {
	state := params.State
	if state == "" {
		state = "all"
	}
	q := url.Values{}
	q.Set("state", state)
	q.Set("per_page", "100")
	q.Set("order_by", "updated_at")
	q.Set("sort", "asc")
	if params.UpdatedAfter != "" {
		q.Set("updated_after", params.UpdatedAfter)
	}
	path := fmt.Sprintf("/projects/%d/issues?%s", projectID, q.Encode())

	var all []Issue
	err := iteratePages[Issue](ctx, c, token, path, func(batch []Issue) error {
		all = append(all, batch...)
		return nil
	})
	return all, err
}
