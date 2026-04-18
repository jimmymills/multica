package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SubscriberResponse is the JSON shape returned for each issue subscriber.
type SubscriberResponse struct {
	IssueID   string `json:"issue_id"`
	UserType  string `json:"user_type"`
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

func subscriberToResponse(s db.IssueSubscriber) SubscriberResponse {
	return SubscriberResponse{
		IssueID:   uuidToString(s.IssueID),
		UserType:  s.UserType,
		UserID:    uuidToString(s.UserID),
		Reason:    s.Reason,
		CreatedAt: timestampToString(s.CreatedAt),
	}
}

// ListIssueSubscribers returns all subscribers for an issue.
func (h *Handler) ListIssueSubscribers(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	subscribers, err := h.Queries.ListIssueSubscribers(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subscribers")
		return
	}

	resp := make([]SubscriberResponse, len(subscribers))
	for i, s := range subscribers {
		resp[i] = subscriberToResponse(s)
	}

	writeJSON(w, http.StatusOK, resp)
}

// SubscribeToIssue subscribes a user to an issue with reason "manual".
// If request body contains user_id, subscribes that user; otherwise subscribes the caller.
func (h *Handler) SubscribeToIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	// Default target: the caller, derived via resolveActor so an agent caller
	// (X-Agent-ID set) subscribes itself rather than the underlying member.
	callerActorType, callerActorID := h.resolveActor(r, requestUserID(r), workspaceID)
	targetUserType := callerActorType
	targetUserID := callerActorID
	var req struct {
		UserID   *string `json:"user_id"`
		UserType *string `json:"user_type"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.UserID != nil && *req.UserID != "" {
		targetUserID = *req.UserID
	}
	if req.UserType != nil && *req.UserType != "" {
		targetUserType = *req.UserType
	}

	if !h.isWorkspaceEntity(r.Context(), targetUserType, targetUserID, workspaceID) {
		writeError(w, http.StatusForbidden, "target user is not a member of this workspace")
		return
	}

	// Phase 3d write-through: when the workspace has a GitLab connection AND
	// the target subscriber is the caller (no body override) AND the caller is
	// a member, POST the subscribe to GitLab first using the caller's PAT, then
	// upsert the local cache row. Agent subscriptions stay Multica-only —
	// GitLab has no concept of agent subscribers. A body-override that targets
	// a different user also falls through to the legacy path, because GitLab's
	// subscribe endpoint only acts on the PAT holder.
	isSelfSubscribe := targetUserType == callerActorType && targetUserID == callerActorID
	if h.GitlabEnabled && h.GitlabResolver != nil && isSelfSubscribe && targetUserType != "agent" {
		_, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), issue.WorkspaceID)
		if wsErr == nil {
			if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid {
				slog.Error("gitlab connected workspace but issue has no gitlab refs",
					"issue_id", issueID, "workspace_id", workspaceID)
				writeError(w, http.StatusBadGateway, "issue not linked to gitlab")
				return
			}

			token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), workspaceID, callerActorType, callerActorID)
			if tokErr != nil {
				slog.Error("resolve gitlab token", "error", tokErr, "workspace_id", workspaceID)
				writeError(w, http.StatusBadGateway, "could not resolve gitlab token")
				return
			}

			if err := h.Gitlab.Subscribe(r.Context(), token,
				issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32)); err != nil {
				slog.Error("gitlab subscribe", "error", err, "issue_id", issueID)
				writeError(w, http.StatusBadGateway, "gitlab subscribe failed")
				return
			}

			if err := h.Queries.AddIssueSubscriber(r.Context(), db.AddIssueSubscriberParams{
				IssueID:  issue.ID,
				UserType: targetUserType,
				UserID:   parseUUID(targetUserID),
				Reason:   "manual",
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to subscribe")
				return
			}

			h.publish(protocol.EventSubscriberAdded, workspaceID, callerActorType, callerActorID, map[string]any{
				"issue_id":  issueID,
				"user_type": targetUserType,
				"user_id":   targetUserID,
				"reason":    "manual",
			})

			writeJSON(w, http.StatusOK, map[string]bool{"subscribed": true})
			return
		}
		// wsErr != nil → fall through to legacy path (non-connected workspace).
	}

	err := h.Queries.AddIssueSubscriber(r.Context(), db.AddIssueSubscriberParams{
		IssueID:  issue.ID,
		UserType: targetUserType,
		UserID:   parseUUID(targetUserID),
		Reason:   "manual",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to subscribe")
		return
	}

	h.publish(protocol.EventSubscriberAdded, workspaceID, callerActorType, callerActorID, map[string]any{
		"issue_id":  issueID,
		"user_type": targetUserType,
		"user_id":   targetUserID,
		"reason":    "manual",
	})

	writeJSON(w, http.StatusOK, map[string]bool{"subscribed": true})
}

// UnsubscribeFromIssue removes a user's subscription from an issue.
// If request body contains user_id, unsubscribes that user; otherwise unsubscribes the caller.
func (h *Handler) UnsubscribeFromIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	// Default target: the caller, derived via resolveActor so an agent caller
	// (X-Agent-ID set) unsubscribes itself rather than the underlying member.
	callerActorType, callerActorID := h.resolveActor(r, requestUserID(r), workspaceID)
	targetUserType := callerActorType
	targetUserID := callerActorID
	var req struct {
		UserID   *string `json:"user_id"`
		UserType *string `json:"user_type"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.UserID != nil && *req.UserID != "" {
		targetUserID = *req.UserID
	}
	if req.UserType != nil && *req.UserType != "" {
		targetUserType = *req.UserType
	}

	if !h.isWorkspaceEntity(r.Context(), targetUserType, targetUserID, workspaceID) {
		writeError(w, http.StatusForbidden, "target user is not a member of this workspace")
		return
	}

	// Phase 3d write-through: when the workspace has a GitLab connection AND
	// the target subscriber is the caller (no body override) AND the caller is
	// a member, POST the unsubscribe to GitLab first using the caller's PAT,
	// then remove the local cache row. Agent unsubscribes stay Multica-only —
	// GitLab has no concept of agent subscribers. A body-override that
	// targets a different user falls through to the legacy path, because
	// GitLab's unsubscribe endpoint only acts on the PAT holder.
	isSelfUnsubscribe := targetUserType == callerActorType && targetUserID == callerActorID
	if h.GitlabEnabled && h.GitlabResolver != nil && isSelfUnsubscribe && targetUserType != "agent" {
		_, wsErr := h.Queries.GetWorkspaceGitlabConnection(r.Context(), issue.WorkspaceID)
		if wsErr == nil {
			if !issue.GitlabIid.Valid || !issue.GitlabProjectID.Valid {
				slog.Error("gitlab connected workspace but issue has no gitlab refs",
					"issue_id", issueID, "workspace_id", workspaceID)
				writeError(w, http.StatusBadGateway, "issue not linked to gitlab")
				return
			}

			token, _, tokErr := h.GitlabResolver.ResolveTokenForWrite(r.Context(), workspaceID, callerActorType, callerActorID)
			if tokErr != nil {
				slog.Error("resolve gitlab token", "error", tokErr, "workspace_id", workspaceID)
				writeError(w, http.StatusBadGateway, "could not resolve gitlab token")
				return
			}

			if err := h.Gitlab.Unsubscribe(r.Context(), token,
				issue.GitlabProjectID.Int64, int(issue.GitlabIid.Int32)); err != nil {
				slog.Error("gitlab unsubscribe", "error", err, "issue_id", issueID)
				writeError(w, http.StatusBadGateway, "gitlab unsubscribe failed")
				return
			}

			if err := h.Queries.RemoveIssueSubscriber(r.Context(), db.RemoveIssueSubscriberParams{
				IssueID:  issue.ID,
				UserType: targetUserType,
				UserID:   parseUUID(targetUserID),
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to unsubscribe")
				return
			}

			h.publish(protocol.EventSubscriberRemoved, workspaceID, callerActorType, callerActorID, map[string]any{
				"issue_id":  issueID,
				"user_type": targetUserType,
				"user_id":   targetUserID,
			})

			writeJSON(w, http.StatusOK, map[string]bool{"subscribed": false})
			return
		}
		// wsErr != nil → fall through to legacy path (non-connected workspace).
	}

	err := h.Queries.RemoveIssueSubscriber(r.Context(), db.RemoveIssueSubscriberParams{
		IssueID:  issue.ID,
		UserType: targetUserType,
		UserID:   parseUUID(targetUserID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unsubscribe")
		return
	}

	h.publish(protocol.EventSubscriberRemoved, workspaceID, callerActorType, callerActorID, map[string]any{
		"issue_id":  issueID,
		"user_type": targetUserType,
		"user_id":   targetUserID,
	})

	writeJSON(w, http.StatusOK, map[string]bool{"subscribed": false})
}
