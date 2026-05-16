package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

var ErrMissingResolveInput = errors.New("email or slug is required")

type ResolveQuery struct {
	Email string
	Slug  string
}

type Store struct {
	workspaces []Workspace
}

func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return NewStore(config)
}

func NewStore(config Config) (*Store, error) {
	if len(config.Workspaces) == 0 {
		return nil, errors.New("at least one workspace is required")
	}

	seenSlugs := map[string]bool{}
	workspaces := make([]Workspace, 0, len(config.Workspaces))
	for _, rawWorkspace := range config.Workspaces {
		workspace, err := normalizeWorkspace(rawWorkspace)
		if err != nil {
			return nil, err
		}
		if seenSlugs[workspace.Slug] {
			return nil, fmt.Errorf("workspace slug %q is duplicated", workspace.Slug)
		}
		seenSlugs[workspace.Slug] = true
		workspaces = append(workspaces, workspace)
	}

	return &Store{workspaces: workspaces}, nil
}

func (store *Store) Resolve(query ResolveQuery) ([]PublicWorkspace, error) {
	slug := strings.ToLower(strings.TrimSpace(query.Slug))
	if slug != "" {
		return store.resolveBySlug(slug), nil
	}

	domain, ok := domainFromEmail(query.Email)
	if !ok {
		return nil, ErrMissingResolveInput
	}

	return store.resolveByEmailDomain(domain), nil
}

func (store *Store) resolveBySlug(slug string) []PublicWorkspace {
	for _, workspace := range store.workspaces {
		if workspace.Status == StatusActive && workspace.Slug == slug {
			return []PublicWorkspace{workspace.public()}
		}
	}
	return []PublicWorkspace{}
}

func (store *Store) resolveByEmailDomain(domain string) []PublicWorkspace {
	matches := []PublicWorkspace{}
	for _, workspace := range store.workspaces {
		if workspace.Status != StatusActive {
			continue
		}
		for _, allowedDomain := range workspace.EmailDomains {
			if allowedDomain == domain {
				matches = append(matches, workspace.public())
				break
			}
		}
	}
	return matches
}

func domainFromEmail(email string) (string, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(email)), "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
