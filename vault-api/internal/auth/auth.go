package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Authenticator validates Matrix access tokens against a homeserver.
type Authenticator struct {
	synapseURL string
	httpClient *http.Client
}

// New creates an Authenticator that validates tokens against the given Synapse URL.
func New(synapseURL string) *Authenticator {
	return &Authenticator{
		synapseURL: synapseURL,
		httpClient: &http.Client{},
	}
}

// WhoAmI validates an access token and returns the Matrix user ID.
func (a *Authenticator) WhoAmI(accessToken string) (string, error) {
	req, err := http.NewRequest("GET", a.synapseURL+"/_matrix/client/v3/account/whoami", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whoami request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whoami returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode whoami: %w", err)
	}
	if result.UserID == "" {
		return "", fmt.Errorf("empty user_id in whoami response")
	}
	return result.UserID, nil
}

// IsRoomMember checks whether a user is a joined member of a room.
func (a *Authenticator) IsRoomMember(accessToken, roomID, userID string) (bool, error) {
	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/joined_members", a.synapseURL, roomID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("joined_members request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var result struct {
		Joined map[string]interface{} `json:"joined"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode joined_members: %w", err)
	}

	_, isMember := result.Joined[userID]
	return isMember, nil
}
