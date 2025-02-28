package msentraid_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// startMSGraphServerMock creates a mock for the MS Graph server to simulate Graph API responses.
func startMSGraphServerMock(handler http.HandlerFunc) (mockServerURL string, stopFunc func()) {
	if handler == nil {
		handler = simpleGroupHandler
	}
	mockServer := httptest.NewServer(handler)
	return mockServer.URL, mockServer.Close
}

// simpleGroupHandler simulates a successful response with a list of groups.
func simpleGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "displayName": "Group1", "securityEnabled": true},
			{"id": "id2", "displayName": "Group2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// localGroupHandler simulates a successful response with a list of local groups.
func localGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "local-id1", "displayName": "linux-local1", "securityEnabled": true},
			{"id": "local-id2", "displayName": "linux-local2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// mixedGroupHandler simulates a successful response with a list of mixed remote and local groups.
func mixedGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "displayName": "Group1", "securityEnabled": true},
			{"id": "local-id1", "displayName": "linux-local1", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// nonSecurityGroupHandler simulates a successful response with a list of groups including non-security groups.
func nonSecurityGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "displayName": "Group1", "securityEnabled": true},
			{"id": "non-security-id", "displayName": "non-security"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// missingIDGroupHandler simulates a successful response with a list of groups missing the ID field.
func missingIDGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"displayName": "Group1", "securityEnabled": true},
			{"id": "id2", "displayName": "Group2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// missingDisplayNameGroupHandler simulates a successful response with a list of groups missing the displayName field.
func missingDisplayNameGroupHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{
		"value": []map[string]any{
			{"id": "id1", "securityEnabled": true},
			{"id": "id2", "displayName": "Group2", "securityEnabled": true},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// errorGroupHandler simulates an error response from the server.
func errorGroupHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
}
