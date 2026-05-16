package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveWorkspaceBySlug(t *testing.T) {
	store := mustStore(t, Config{Workspaces: []Workspace{
		workspaceFixture("acme", "acme.com"),
	}})

	workspaces, err := store.Resolve(ResolveQuery{Slug: "ACME"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(workspaces))
	}
	if workspaces[0].SecurityMode != SecurityModeEasyE2EE {
		t.Fatalf("expected default security mode %q, got %q", SecurityModeEasyE2EE, workspaces[0].SecurityMode)
	}
	encoded, err := json.Marshal(workspaces[0])
	if err != nil {
		t.Fatalf("public workspace is not JSON serializable: %v", err)
	}
	if strings.Contains(string(encoded), "email_domains") {
		t.Fatalf("public workspace must not expose email domains")
	}
}

func TestResolveWorkspaceByEmailDomain(t *testing.T) {
	store := mustStore(t, Config{Workspaces: []Workspace{
		workspaceFixture("acme", "acme.com"),
		workspaceFixture("beta", "beta.com"),
	}})

	workspaces, err := store.Resolve(ResolveQuery{Email: "ALICE@ACME.COM"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].Slug != "acme" {
		t.Fatalf("expected acme workspace, got %#v", workspaces)
	}
}

func TestResolveWorkspaceReturnsMultipleDomainMatches(t *testing.T) {
	store := mustStore(t, Config{Workspaces: []Workspace{
		workspaceFixture("acme", "example.com"),
		workspaceFixture("contoso", "example.com"),
	}})

	workspaces, err := store.Resolve(ResolveQuery{Email: "owner@example.com"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("expected 2 workspaces for shared email domain, got %d", len(workspaces))
	}
}

func TestResolveWorkspaceSkipsDisabledTenants(t *testing.T) {
	disabled := workspaceFixture("acme", "acme.com")
	disabled.Status = StatusDisabled
	store := mustStore(t, Config{Workspaces: []Workspace{disabled}})

	workspaces, err := store.Resolve(ResolveQuery{Email: "alice@acme.com"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("expected no active workspaces, got %#v", workspaces)
	}
}

func TestResolveWorkspaceRequiresEmailOrSlug(t *testing.T) {
	store := mustStore(t, Config{Workspaces: []Workspace{
		workspaceFixture("acme", "acme.com"),
	}})

	_, err := store.Resolve(ResolveQuery{Email: "not-an-email"})
	if err != ErrMissingResolveInput {
		t.Fatalf("expected ErrMissingResolveInput, got %v", err)
	}
}

func TestNewStoreRejectsInvalidSecurityMode(t *testing.T) {
	workspace := workspaceFixture("acme", "acme.com")
	workspace.SecurityMode = "magic"

	_, err := NewStore(Config{Workspaces: []Workspace{workspace}})
	if err == nil {
		t.Fatal("expected invalid security mode error")
	}
}

func TestNewStoreRejectsDuplicateSlugs(t *testing.T) {
	duplicate := workspaceFixture("acme", "acme.org")
	duplicate.ID = "acme-2"
	_, err := NewStore(Config{Workspaces: []Workspace{
		workspaceFixture("acme", "acme.com"),
		duplicate,
	}})
	if err == nil {
		t.Fatal("expected duplicate slug error")
	}
}

func mustStore(t *testing.T, config Config) *Store {
	t.Helper()
	store, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	return store
}

func workspaceFixture(slug string, emailDomain string) Workspace {
	return Workspace{
		ID:            slug,
		Slug:          slug,
		DisplayName:   slug + " Ltd",
		EmailDomains:  []string{emailDomain},
		HomeserverURL: "https://" + slug + ".matrix.letsyak.com",
		VaultAPIURL:   "https://" + slug + ".vault.letsyak.com",
		Features:      map[string]bool{"vault": true},
	}
}
