package workspace

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"

	IsolationShared    = "shared"
	IsolationDedicated = "dedicated"

	SecurityModeSimple   = "simple"
	SecurityModeEasyE2EE = "easy_e2ee"
	SecurityModeBalanced = "balanced"
	SecurityModeStrict   = "strict"
)

type Config struct {
	Workspaces []Workspace `json:"workspaces"`
}

type Workspace struct {
	ID            string          `json:"id"`
	Slug          string          `json:"slug"`
	DisplayName   string          `json:"display_name"`
	Status        string          `json:"status"`
	EmailDomains  []string        `json:"email_domains"`
	HomeserverURL string          `json:"homeserver_url"`
	VaultAPIURL   string          `json:"vault_api_url"`
	IsolationTier string          `json:"isolation_tier"`
	SecurityMode  string          `json:"security_mode"`
	LoginMethods  []string        `json:"login_methods"`
	Branding      Branding        `json:"branding"`
	Features      map[string]bool `json:"features"`
}

type Branding struct {
	LogoURL        string `json:"logo_url"`
	PrimaryColor   string `json:"primary_color"`
	SecondaryColor string `json:"secondary_color"`
	SupportURL     string `json:"support_url"`
	PrivacyURL     string `json:"privacy_url"`
}

type PublicWorkspace struct {
	ID            string          `json:"id"`
	Slug          string          `json:"slug"`
	DisplayName   string          `json:"display_name"`
	HomeserverURL string          `json:"homeserver_url"`
	VaultAPIURL   string          `json:"vault_api_url"`
	IsolationTier string          `json:"isolation_tier"`
	SecurityMode  string          `json:"security_mode"`
	LoginMethods  []string        `json:"login_methods"`
	Branding      Branding        `json:"branding"`
	Features      map[string]bool `json:"features"`
}

func (workspace Workspace) public() PublicWorkspace {
	return PublicWorkspace{
		ID:            workspace.ID,
		Slug:          workspace.Slug,
		DisplayName:   workspace.DisplayName,
		HomeserverURL: workspace.HomeserverURL,
		VaultAPIURL:   workspace.VaultAPIURL,
		IsolationTier: workspace.IsolationTier,
		SecurityMode:  workspace.SecurityMode,
		LoginMethods:  append([]string(nil), workspace.LoginMethods...),
		Branding:      workspace.Branding,
		Features:      cloneFeatures(workspace.Features),
	}
}

func normalizeWorkspace(workspace Workspace) (Workspace, error) {
	workspace.ID = strings.TrimSpace(workspace.ID)
	workspace.Slug = strings.ToLower(strings.TrimSpace(workspace.Slug))
	workspace.DisplayName = strings.TrimSpace(workspace.DisplayName)
	workspace.Status = strings.ToLower(strings.TrimSpace(workspace.Status))
	workspace.IsolationTier = strings.ToLower(strings.TrimSpace(workspace.IsolationTier))
	workspace.SecurityMode = strings.ToLower(strings.TrimSpace(workspace.SecurityMode))

	if workspace.Status == "" {
		workspace.Status = StatusActive
	}
	if workspace.IsolationTier == "" {
		workspace.IsolationTier = IsolationDedicated
	}
	if workspace.SecurityMode == "" {
		workspace.SecurityMode = SecurityModeEasyE2EE
	}
	if len(workspace.LoginMethods) == 0 {
		workspace.LoginMethods = []string{"password"}
	}

	for i, domain := range workspace.EmailDomains {
		workspace.EmailDomains[i] = strings.ToLower(strings.TrimSpace(domain))
	}

	if workspace.ID == "" {
		return workspace, fmt.Errorf("workspace id is required")
	}
	if workspace.Slug == "" {
		return workspace, fmt.Errorf("workspace %s slug is required", workspace.ID)
	}
	if strings.ContainsAny(workspace.Slug, " @:/") {
		return workspace, fmt.Errorf("workspace %s slug contains invalid characters", workspace.ID)
	}
	if workspace.DisplayName == "" {
		return workspace, fmt.Errorf("workspace %s display_name is required", workspace.ID)
	}
	if !validURL(workspace.HomeserverURL) {
		return workspace, fmt.Errorf("workspace %s homeserver_url must be an absolute http(s) URL", workspace.ID)
	}
	if !validURL(workspace.VaultAPIURL) {
		return workspace, fmt.Errorf("workspace %s vault_api_url must be an absolute http(s) URL", workspace.ID)
	}
	if workspace.Status != StatusActive && workspace.Status != StatusDisabled {
		return workspace, fmt.Errorf("workspace %s status must be active or disabled", workspace.ID)
	}
	if workspace.IsolationTier != IsolationShared && workspace.IsolationTier != IsolationDedicated {
		return workspace, fmt.Errorf("workspace %s isolation_tier must be shared or dedicated", workspace.ID)
	}
	if !validSecurityMode(workspace.SecurityMode) {
		return workspace, fmt.Errorf("workspace %s security_mode is invalid", workspace.ID)
	}

	return workspace, nil
}

func validSecurityMode(mode string) bool {
	switch mode {
	case SecurityModeSimple, SecurityModeEasyE2EE, SecurityModeBalanced, SecurityModeStrict:
		return true
	default:
		return false
	}
}

func validURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func cloneFeatures(features map[string]bool) map[string]bool {
	if len(features) == 0 {
		return map[string]bool{}
	}
	clone := make(map[string]bool, len(features))
	for key, value := range features {
		clone[key] = value
	}
	return clone
}
