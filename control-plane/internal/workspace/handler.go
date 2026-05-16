package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
)

type Handler struct {
	store *Store
}

type resolveResponse struct {
	Workspaces []PublicWorkspace `json:"workspaces"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (handler *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/workspaces/resolve", handler.Resolve)
}

func (handler *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	workspaces, err := handler.store.Resolve(ResolveQuery{
		Email: r.URL.Query().Get("email"),
		Slug:  r.URL.Query().Get("slug"),
	})
	if errors.Is(err, ErrMissingResolveInput) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "workspace resolution failed"})
		return
	}

	writeJSON(w, http.StatusOK, resolveResponse{Workspaces: workspaces})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
