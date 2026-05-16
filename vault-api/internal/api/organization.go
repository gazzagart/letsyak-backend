package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/garethmaybery/letsyak-vault-api/internal/db"
	"github.com/garethmaybery/letsyak-vault-api/internal/storage"

	"github.com/go-chi/chi/v5"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9-]+`)

func (h *Handler) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	if err := h.ensureVaultUser(r, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to provision owner")
		log.Printf("ensure owner vault user error for %s: %v", userID, err)
		return
	}

	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}

	org, err := h.db.CreateOrganization(name, slug, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, organizationMembershipResponse(org))
}

func (h *Handler) ListOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.db.ListOrganizationsForUser(getUserID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list organisations")
		log.Printf("ListOrganizationsForUser error: %v", err)
		return
	}

	result := make([]map[string]interface{}, 0, len(orgs))
	for _, org := range orgs {
		result = append(result, organizationMembershipResponse(org))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) ListOrganizationMembers(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	if _, ok := h.requireOrgAdmin(w, r, orgID); !ok {
		return
	}

	members, err := h.db.ListOrganizationMembers(orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list organisation members")
		log.Printf("ListOrganizationMembers error for %s: %v", orgID, err)
		return
	}
	writeJSON(w, http.StatusOK, organizationMembersResponse(members))
}

func (h *Handler) AddOrganizationMember(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	actorRole, ok := h.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}

	var req struct {
		MatrixUserID string `json:"matrix_user_id"`
		Role         string `json:"role,omitempty"`
		AssignedTier string `json:"assigned_tier,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	targetUserID := strings.TrimSpace(req.MatrixUserID)
	if targetUserID == "" {
		writeError(w, http.StatusBadRequest, "matrix_user_id is required")
		return
	}
	if !isMatrixUserID(targetUserID) {
		writeError(w, http.StatusBadRequest, "matrix_user_id must look like @user:server")
		return
	}
	role, err := db.NormalizeOrgRole(req.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !db.CanAssignOrgRole(actorRole, role) {
		writeError(w, http.StatusForbidden, "not allowed to assign that role")
		return
	}
	tier := req.AssignedTier
	if strings.TrimSpace(tier) == "" {
		tier = db.TierFree
	}
	if err := h.ensureVaultUser(r, targetUserID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to provision member")
		log.Printf("ensure member vault user error for %s: %v", targetUserID, err)
		return
	}

	member, err := h.db.AddOrganizationMember(orgID, getUserID(r), targetUserID, role, tier)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, organizationMemberResponse(member))
}

func (h *Handler) UpdateOrganizationMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	targetUserID, err := pathUserID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid matrix user id")
		return
	}
	actorRole, ok := h.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}
	target, err := h.db.GetOrganizationMember(orgID, targetUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}
	if target == nil || target.Status != db.OrgMemberStatusActive {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	newRole, err := db.NormalizeOrgRole(req.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !db.CanManageOrgMember(actorRole, target.Role) || !db.CanAssignOrgRole(actorRole, newRole) {
		writeError(w, http.StatusForbidden, "not allowed to change that role")
		return
	}

	member, err := h.db.UpdateOrganizationMemberRole(orgID, getUserID(r), targetUserID, newRole)
	if err != nil {
		h.writeOrgMutationError(w, err)
		return
	}
	if member == nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	writeJSON(w, http.StatusOK, organizationMemberResponse(member))
}

func (h *Handler) UpdateOrganizationMemberTier(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	targetUserID, err := pathUserID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid matrix user id")
		return
	}
	actorRole, ok := h.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}
	target, err := h.db.GetOrganizationMember(orgID, targetUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}
	if target == nil || target.Status != db.OrgMemberStatusActive {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if !db.CanManageOrgMember(actorRole, target.Role) {
		writeError(w, http.StatusForbidden, "not allowed to change that member")
		return
	}

	var req struct {
		Tier string `json:"assigned_tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	member, err := h.db.UpdateOrganizationMemberTier(orgID, getUserID(r), targetUserID, req.Tier)
	if err != nil {
		h.writeOrgMutationError(w, err)
		return
	}
	if member == nil {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	writeJSON(w, http.StatusOK, organizationMemberResponse(member))
}

func (h *Handler) RemoveOrganizationMember(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	targetUserID, err := pathUserID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid matrix user id")
		return
	}
	actorRole, ok := h.requireOrgAdmin(w, r, orgID)
	if !ok {
		return
	}
	target, err := h.db.GetOrganizationMember(orgID, targetUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}
	if target == nil || target.Status != db.OrgMemberStatusActive {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if !db.CanManageOrgMember(actorRole, target.Role) {
		writeError(w, http.StatusForbidden, "not allowed to remove that member")
		return
	}

	removed, err := h.db.RemoveOrganizationMember(orgID, getUserID(r), targetUserID)
	if err != nil {
		h.writeOrgMutationError(w, err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) GetOrganizationUsage(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgID")
	if _, ok := h.requireOrgAdmin(w, r, orgID); !ok {
		return
	}
	usage, err := h.db.GetOrganizationUsage(orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load organisation usage")
		log.Printf("GetOrganizationUsage error for %s: %v", orgID, err)
		return
	}
	writeJSON(w, http.StatusOK, organizationUsageResponse(usage))
}

func (h *Handler) requireOrgAdmin(w http.ResponseWriter, r *http.Request, orgID string) (string, bool) {
	role, ok, err := h.db.GetOrganizationRole(orgID, getUserID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check organisation role")
		return "", false
	}
	if !ok || !db.IsOrgAdminRole(role) {
		writeError(w, http.StatusForbidden, "organisation admin access required")
		return "", false
	}
	return role, true
}

func (h *Handler) ensureVaultUser(r *http.Request, matrixUserID string) error {
	_, err := h.db.GetOrCreateUser(matrixUserID, storage.BucketName(matrixUserID))
	return err
}

func (h *Handler) writeOrgMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrLastOwner) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func pathUserID(r *http.Request) (string, error) {
	return url.PathUnescape(chi.URLParam(r, "matrixUserID"))
}

func isMatrixUserID(value string) bool {
	return strings.HasPrefix(value, "@") && strings.Contains(value, ":") && !strings.Contains(value, "/")
}

func organizationMembershipResponse(org *db.OrganizationMembership) map[string]interface{} {
	return map[string]interface{}{
		"id":                  org.ID,
		"name":                org.Name,
		"slug":                org.Slug,
		"owner_user_id":       org.OwnerUserID,
		"storage_plan":        org.StoragePlan,
		"seat_limit":          org.SeatLimit,
		"storage_quota_bytes": org.StorageQuotaBytes,
		"role":                org.Role,
		"status":              org.Status,
		"assigned_tier":       org.AssignedTier,
		"created_at":          org.CreatedAt,
		"updated_at":          org.UpdatedAt,
	}
}

func organizationMembersResponse(members []*db.OrganizationMember) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(members))
	for _, member := range members {
		result = append(result, organizationMemberResponse(member))
	}
	return result
}

func organizationMemberResponse(member *db.OrganizationMember) map[string]interface{} {
	tierLabel := formatQuotaLabel(member.QuotaBytes)
	if info, err := db.TierInfoFor(member.AssignedTier); err == nil {
		tierLabel = info.LimitLabel
	}
	return map[string]interface{}{
		"org_id":         member.OrgID,
		"matrix_user_id": member.MatrixUserID,
		"role":           member.Role,
		"status":         member.Status,
		"assigned_tier":  member.AssignedTier,
		"used_bytes":     member.UsedBytes,
		"quota_bytes":    member.QuotaBytes,
		"vault_tier":     member.VaultTier,
		"limit_label":    tierLabel,
		"is_over_quota":  member.UsedBytes > member.QuotaBytes,
		"created_at":     member.CreatedAt,
		"removed_at":     member.RemovedAt,
	}
}

func organizationUsageResponse(usage *db.OrganizationUsage) map[string]interface{} {
	return map[string]interface{}{
		"org_id":              usage.OrgID,
		"active_members":      usage.ActiveMembers,
		"assigned_plus_seats": usage.AssignedPlusSeats,
		"total_used_bytes":    usage.TotalUsedBytes,
		"total_quota_bytes":   usage.TotalQuotaBytes,
		"over_quota_members":  usage.OverQuotaMembers,
		"members":             organizationMembersResponse(usage.Members),
	}
}

func slugify(value string) string {
	slug := strings.ToLower(strings.TrimSpace(value))
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = nonSlugChars.ReplaceAllString(slug, "")
	slug = strings.Trim(slug, "-")
	return slug
}
