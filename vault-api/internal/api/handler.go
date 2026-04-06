package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/garethmaybery/letsyak-vault-api/internal/auth"
	"github.com/garethmaybery/letsyak-vault-api/internal/db"
	"github.com/garethmaybery/letsyak-vault-api/internal/storage"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const userIDKey contextKey = "userID"

// Handler holds references to all dependencies.
type Handler struct {
	store     *storage.Client
	db        *db.Database
	auth      *auth.Authenticator
	publicURL string
}

// NewHandler creates a new Handler.
func NewHandler(store *storage.Client, database *db.Database, authenticator *auth.Authenticator, publicURL string) *Handler {
	return &Handler{
		store:     store,
		db:        database,
		auth:      authenticator,
		publicURL: publicURL,
	}
}

// AuthMiddleware validates the Matrix access token and stores the user ID in context.
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		userID, err := h.auth.WhoAmI(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getUserID(r *http.Request) string {
	return r.Context().Value(userIDKey).(string)
}

// ── Provisioning ──────────────────────────────────────────────────

func (h *Handler) Provision(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	bucketName := storage.BucketName(userID)

	if err := h.store.EnsureBucket(r.Context(), bucketName); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create bucket")
		log.Printf("EnsureBucket error for %s: %v", userID, err)
		return
	}

	if _, err := h.db.GetOrCreateUser(userID, bucketName); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to provision user")
		log.Printf("GetOrCreateUser error for %s: %v", userID, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Quota ─────────────────────────────────────────────────────────

func (h *Handler) GetQuota(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	// Recalculate actual usage from storage
	size, err := h.store.GetBucketSize(r.Context(), user.BucketName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to calculate usage")
		log.Printf("GetBucketSize error for %s: %v", userID, err)
		return
	}
	if size != user.UsedBytes {
		_ = h.db.UpdateUsedBytes(userID, size)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"used_bytes":  size,
		"total_bytes": user.QuotaBytes,
		"tier":        user.Tier,
	})
}

// ── Files ─────────────────────────────────────────────────────────

func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	path := r.URL.Query().Get("path")
	files, err := h.store.ListObjects(r.Context(), user.BucketName, path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list files")
		log.Printf("ListObjects error for %s: %v", userID, err)
		return
	}

	if files == nil {
		files = []storage.FileInfo{}
	}
	writeJSON(w, http.StatusOK, files)
}

func (h *Handler) GetUploadURL(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	var req struct {
		Path     string `json:"path"`
		FileName string `json:"file_name"`
		FileSize int64  `json:"file_size"`
		MIMEType string `json:"mime_type,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.FileName == "" {
		writeError(w, http.StatusBadRequest, "file_name is required")
		return
	}

	// Check quota
	currentSize, err := h.store.GetBucketSize(r.Context(), user.BucketName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check quota")
		return
	}
	if currentSize+req.FileSize > user.QuotaBytes {
		writeError(w, http.StatusForbidden, "storage quota exceeded")
		return
	}

	objectKey := buildObjectKey(req.Path, req.FileName)
	url, err := h.store.PresignedPutURL(r.Context(), user.BucketName, objectKey, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate upload URL")
		log.Printf("PresignedPutURL error for %s: %v", userID, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"upload_url": url})
}

func (h *Handler) GetDownloadURL(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	url, err := h.store.PresignedGetURL(r.Context(), user.BucketName, req.Path, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate download URL")
		log.Printf("PresignedGetURL error for %s: %v", userID, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"download_url": url})
}

func (h *Handler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.store.CreateFolder(r.Context(), user.BucketName, req.Path); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create folder")
		log.Printf("CreateFolder error for %s: %v", userID, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Folders have a trailing slash — delete all contents recursively
	if strings.HasSuffix(path, "/") {
		if err := h.store.DeletePrefix(r.Context(), user.BucketName, path); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete folder")
			log.Printf("DeletePrefix error for %s: %v", userID, err)
			return
		}
	} else {
		if err := h.store.DeleteObject(r.Context(), user.BucketName, path); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete file")
			log.Printf("DeleteObject error for %s: %v", userID, err)
			return
		}
	}

	// Update usage
	size, _ := h.store.GetBucketSize(r.Context(), user.BucketName)
	_ = h.db.UpdateUsedBytes(userID, size)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) MoveFile(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.store.MoveObject(r.Context(), user.BucketName, req.From, req.To); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to move file")
		log.Printf("MoveObject error for %s: %v", userID, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Shares ────────────────────────────────────────────────────────

func (h *Handler) CreateShare(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := h.db.GetUser(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusNotFound, "user not provisioned")
		return
	}

	var req struct {
		ObjectKey    string  `json:"object_key"`
		FileName     string  `json:"file_name"`
		FileSize     int64   `json:"file_size"`
		MIMEType     *string `json:"mime_type,omitempty"`
		ShareType    string  `json:"share_type"`
		TargetID     *string `json:"target_id,omitempty"`
		Password     *string `json:"password,omitempty"`
		ExpiresAt    *string `json:"expires_at,omitempty"`
		MaxDownloads *int    `json:"max_downloads,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	share := &db.Share{
		OwnerUserID:  userID,
		ObjectKey:    req.ObjectKey,
		FileName:     req.FileName,
		FileSize:     req.FileSize,
		MIMEType:     req.MIMEType,
		ShareType:    req.ShareType,
		TargetID:     req.TargetID,
		MaxDownloads: req.MaxDownloads,
	}

	if req.Password != nil && *req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		hashStr := string(hash)
		share.PasswordHash = &hashStr
	}

	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_at format")
			return
		}
		share.ExpiresAt = &t
	}

	if err := h.db.CreateShare(share); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create share")
		log.Printf("CreateShare error for %s: %v", userID, err)
		return
	}

	vaultURL := h.publicURL + "/share/" + share.ShareID

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"share_id":      share.ShareID,
		"file_name":     share.FileName,
		"file_size":     share.FileSize,
		"vault_url":     vaultURL,
		"owner_user_id": userID,
		"target_id":     share.TargetID,
		"expires_at":    share.ExpiresAt,
		"is_revoked":    false,
	})
}

func (h *Handler) GetShare(w http.ResponseWriter, r *http.Request) {
	shareID := chi.URLParam(r, "shareID")
	share, err := h.db.GetShare(shareID)
	if err != nil || share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	if share.IsRevoked {
		writeError(w, http.StatusGone, "share has been revoked")
		return
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		writeError(w, http.StatusGone, "share has expired")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"share_id":      share.ShareID,
		"file_name":     share.FileName,
		"file_size":     share.FileSize,
		"owner_user_id": share.OwnerUserID,
		"share_type":    share.ShareType,
		"target_id":     share.TargetID,
		"expires_at":    share.ExpiresAt,
		"is_revoked":    share.IsRevoked,
	})
}

func (h *Handler) DownloadShare(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	shareID := chi.URLParam(r, "shareID")

	share, err := h.db.GetShare(shareID)
	if err != nil || share == nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	if share.IsRevoked {
		writeError(w, http.StatusGone, "share has been revoked")
		return
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		writeError(w, http.StatusGone, "share has expired")
		return
	}
	if share.MaxDownloads != nil && share.DownloadCount >= *share.MaxDownloads {
		writeError(w, http.StatusGone, "download limit reached")
		return
	}

	// ACL: If share is scoped to a room, verify the requesting user is a member
	if share.ShareType == "room" && share.TargetID != nil {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		isMember, err := h.auth.IsRoomMember(token, *share.TargetID, userID)
		if err != nil || !isMember {
			writeError(w, http.StatusForbidden, "not a member of the target room")
			return
		}
	}

	// Look up owner bucket to generate presigned URL
	owner, err := h.db.GetUser(share.OwnerUserID)
	if err != nil || owner == nil {
		writeError(w, http.StatusInternalServerError, "owner not found")
		return
	}

	url, err := h.store.PresignedGetURL(r.Context(), owner.BucketName, share.ObjectKey, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate download URL")
		log.Printf("PresignedGetURL error for share %s: %v", shareID, err)
		return
	}

	_ = h.db.IncrementDownloadCount(shareID)

	writeJSON(w, http.StatusOK, map[string]string{"download_url": url})
}

func (h *Handler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	shareID := chi.URLParam(r, "shareID")

	if err := h.db.RevokeShare(shareID, userID); err != nil {
		writeError(w, http.StatusNotFound, "share not found or not owned by you")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ListMyShares(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	shares, err := h.db.ListSharesByOwner(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list shares")
		log.Printf("ListSharesByOwner error for %s: %v", userID, err)
		return
	}

	result := make([]map[string]interface{}, 0, len(shares))
	for _, s := range shares {
		result = append(result, map[string]interface{}{
			"share_id":       s.ShareID,
			"file_name":      s.FileName,
			"file_size":      s.FileSize,
			"vault_url":      h.publicURL + "/share/" + s.ShareID,
			"owner_user_id":  s.OwnerUserID,
			"target_id":      s.TargetID,
			"expires_at":     s.ExpiresAt,
			"is_revoked":     s.IsRevoked,
			"download_count": s.DownloadCount,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// PublicSharePage serves a basic download page for unauthenticated share links.
func (h *Handler) PublicSharePage(w http.ResponseWriter, r *http.Request) {
	shareID := chi.URLParam(r, "shareID")

	share, err := h.db.GetShare(shareID)
	if err != nil || share == nil {
		http.Error(w, "Share not found", http.StatusNotFound)
		return
	}
	if share.IsRevoked {
		http.Error(w, "This share has been revoked", http.StatusGone)
		return
	}
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		http.Error(w, "This share has expired", http.StatusGone)
		return
	}

	// For password-protected shares, require password via query param
	if share.PasswordHash != nil {
		password := r.URL.Query().Get("password")
		if password == "" {
			http.Error(w, "Password required", http.StatusUnauthorized)
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(*share.PasswordHash), []byte(password)); err != nil {
			http.Error(w, "Invalid password", http.StatusForbidden)
			return
		}
	}

	// Look up owner to get bucket
	owner, err := h.db.GetUser(share.OwnerUserID)
	if err != nil || owner == nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	url, err := h.store.PresignedGetURL(r.Context(), owner.BucketName, share.ObjectKey, 15*time.Minute)
	if err != nil {
		http.Error(w, "Failed to generate download", http.StatusInternalServerError)
		return
	}

	_ = h.db.IncrementDownloadCount(shareID)

	// Redirect to presigned URL
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// ── Helpers ───────────────────────────────────────────────────────

func buildObjectKey(path, fileName string) string {
	if path == "" || path == "/" {
		return fileName
	}
	path = strings.TrimPrefix(path, "/")
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path + fileName
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
