package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestJoinedRoomsReturnsJoinedRoomIDsAndForwardsToken(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewEncoder(w).Encode(map[string][]string{
			"joined_rooms": {"!room-one:example.test", "!room-two:example.test"},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	rooms, err := New(server.URL).JoinedRooms("matrix-token")
	if err != nil {
		t.Fatalf("JoinedRooms returned error: %v", err)
	}

	wantRooms := []string{"!room-one:example.test", "!room-two:example.test"}
	if !reflect.DeepEqual(rooms, wantRooms) {
		t.Fatalf("JoinedRooms rooms = %#v, want %#v", rooms, wantRooms)
	}
	if gotPath != "/_matrix/client/v3/joined_rooms" {
		t.Fatalf("request path = %q, want joined_rooms endpoint", gotPath)
	}
	if gotAuthorization != "Bearer matrix-token" {
		t.Fatalf("Authorization header = %q, want bearer token", gotAuthorization)
	}
}

func TestJoinedRoomsReturnsErrorForNonOKResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "session expired", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := New(server.URL).JoinedRooms("expired-token")
	if err == nil {
		t.Fatal("JoinedRooms returned nil error for unauthorized response")
	}
	if !strings.Contains(err.Error(), "joined_rooms returned 401") {
		t.Fatalf("JoinedRooms error = %q, want status context", err.Error())
	}
}
