package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) listApprovals(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	approvals, err := h.db.ListApprovalRequests(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("status")), intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals})
}

func (h *handler) createApproval(w http.ResponseWriter, r *http.Request) {
	var input store.CreateApprovalRequestInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	input.Metadata = approvalActorMetadata(input.Metadata, "client_requested_by", input.RequestedBy)
	input.RequestedBy = authenticatedApprovalActor(r)
	approval, err := h.db.CreateApprovalRequest(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval})
}

func (h *handler) approveApproval(w http.ResponseWriter, r *http.Request) {
	h.decideApproval(w, r, true)
}

func (h *handler) rejectApproval(w http.ResponseWriter, r *http.Request) {
	h.decideApproval(w, r, false)
}

func (h *handler) decideApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	approval, err := h.db.GetApprovalRequest(r.Context(), r.PathValue("approvalId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionOps, approval.Scope) {
		return
	}
	var input store.DecideApprovalRequestInput
	_ = json.NewDecoder(r.Body).Decode(&input)
	input.Metadata = approvalActorMetadata(input.Metadata, "client_decided_by", input.DecidedBy)
	input.DecidedBy = authenticatedApprovalActor(r)
	if approved {
		approval, err = h.db.ApproveApprovalRequest(r.Context(), approval.ID, input)
	} else {
		approval, err = h.db.RejectApprovalRequest(r.Context(), approval.ID, input)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval})
}

func authenticatedApprovalActor(r *http.Request) string {
	principal := principalFromContext(r.Context())
	if principal == nil || strings.TrimSpace(principal.token) == "" {
		return "anonymous-dev"
	}
	ref := hashedRateLimitKey("api-key", principal.token)
	const maxActorRef = len("api-key:sha256:") + 16
	if len(ref) > maxActorRef {
		return ref[:maxActorRef]
	}
	return ref
}

func approvalActorMetadata(metadata map[string]any, key, clientValue string) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if clientValue = strings.TrimSpace(clientValue); clientValue != "" {
		metadata[key] = clientValue
	}
	return metadata
}
