package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveHandlerReturnsWorkspaceMatches(t *testing.T) {
	store := mustStore(t, Config{Workspaces: []Workspace{
		workspaceFixture("acme", "acme.com"),
	}})
	handler := NewHandler(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/resolve?email=alice@acme.com", nil)
	handler.Resolve(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", recorder.Code)
	}

	var response resolveResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(response.Workspaces) != 1 || response.Workspaces[0].Slug != "acme" {
		t.Fatalf("expected acme workspace, got %#v", response.Workspaces)
	}
}

func TestResolveHandlerRejectsMissingQuery(t *testing.T) {
	store := mustStore(t, Config{Workspaces: []Workspace{
		workspaceFixture("acme", "acme.com"),
	}})
	handler := NewHandler(store)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/resolve", nil)
	handler.Resolve(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", recorder.Code)
	}
}
