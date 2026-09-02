package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

/*
Authorization tests.

Two rules for this file:

 1. every role is tested against every sensitive action, allowed *and* denied.
    A test suite that only checks the happy path proves nothing about access
    control.
 2. 401 and 403 are asserted separately. Collapsing them hides the difference
    between "you are not logged in" and "you are, and you still may not".
*/

func principalWith(roles ...Role) Principal {
	return Principal{UserID: 2, Email: "test@example.com", Roles: roles}
}

func TestDocumentPolicyMatrix(t *testing.T) {
	t.Parallel()

	ownDraft := &Document{ID: 1, OwnerID: 2, Published: false}
	othersDraft := &Document{ID: 2, OwnerID: 3, Published: false}
	published := &Document{ID: 3, OwnerID: 3, Published: true}

	tests := []struct {
		role     Role
		action   Action
		document *Document
		allowed  bool
	}{
		// guest: read published only
		{RoleGuest, ActionRead, published, true},
		{RoleGuest, ActionRead, othersDraft, false},
		{RoleGuest, ActionCreate, nil, false},
		{RoleGuest, ActionUpdate, ownDraft, false},
		{RoleGuest, ActionDelete, published, false},

		// member: own documents only
		{RoleMember, ActionRead, published, true},
		{RoleMember, ActionRead, ownDraft, true},
		{RoleMember, ActionRead, othersDraft, false},
		{RoleMember, ActionCreate, nil, true},
		{RoleMember, ActionUpdate, ownDraft, true},
		{RoleMember, ActionUpdate, othersDraft, false},
		{RoleMember, ActionPublish, ownDraft, false},
		{RoleMember, ActionDelete, ownDraft, false},

		// editor: any document, but no deletion
		{RoleEditor, ActionRead, othersDraft, true},
		{RoleEditor, ActionUpdate, othersDraft, true},
		{RoleEditor, ActionPublish, othersDraft, true},
		{RoleEditor, ActionDelete, othersDraft, false},

		// admin: everything
		{RoleAdmin, ActionRead, othersDraft, true},
		{RoleAdmin, ActionUpdate, othersDraft, true},
		{RoleAdmin, ActionPublish, othersDraft, true},
		{RoleAdmin, ActionDelete, othersDraft, true},
	}

	for _, test := range tests {
		name := string(test.role) + "/" + string(test.action)

		t.Run(name, func(t *testing.T) {
			err := AuthorizeDocument(principalWith(test.role), test.action, test.document)

			if test.allowed && err != nil {
				t.Fatalf("expected allow, got %v", err)
			}

			if !test.allowed {
				if err == nil {
					t.Fatal("expected deny, got allow")
				}

				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("err = %v, want ErrForbidden", err)
				}
			}
		})
	}
}

func TestUnknownActionIsDenied(t *testing.T) {
	t.Parallel()

	err := AuthorizeDocument(principalWith(RoleAdmin), Action("teleport"), &Document{})

	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden - the policy must default to deny", err)
	}
}

func TestUpdateWithoutDocumentIsDenied(t *testing.T) {
	t.Parallel()

	if err := AuthorizeDocument(principalWith(RoleEditor), ActionUpdate, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestPrincipalPermissionsAreDerivedFromRoles(t *testing.T) {
	t.Parallel()

	member := principalWith(RoleMember)

	if !member.Can(PermDocumentCreate) {
		t.Fatal("member cannot create documents")
	}

	if member.Can(PermDocumentDelete) || member.Can(PermUserSuspend) {
		t.Fatal("member has an admin permission")
	}

	// Multiple roles are additive.
	both := principalWith(RoleMember, RoleEditor)

	if !both.Can(PermDocumentPublish) {
		t.Fatal("member+editor cannot publish")
	}

	if len(both.Permissions()) <= len(member.Permissions()) {
		t.Fatal("adding a role did not add permissions")
	}
}

//
// HTTP LEVEL: 401 vs 403
//

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer((&API{documents: NewDocumentStore()}).Routes())

	t.Cleanup(server.Close)

	return server
}

func call(t *testing.T, server *httptest.Server, method, path, token string, body any) (int, []byte) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, buffer.Bytes()
}

func TestEndpointAuthorization(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	tests := []struct {
		name   string
		method string
		path   string
		token  string
		body   any
		want   int
	}{
		// No identity at all: 401, never 403.
		{"anonymous list", http.MethodGet, "/documents", "", nil, http.StatusUnauthorized},
		{"unknown token", http.MethodGet, "/documents", "made-up-token", nil, http.StatusUnauthorized},
		{"anonymous delete", http.MethodDelete, "/documents/1", "", nil, http.StatusUnauthorized},

		// Identity known, permission missing: 403.
		{"guest cannot create", http.MethodPost, "/documents", "guest-token",
			map[string]string{"title": "x"}, http.StatusForbidden},
		{"member cannot delete", http.MethodDelete, "/documents/1", "member-token", nil, http.StatusForbidden},
		{"editor cannot delete", http.MethodDelete, "/documents/1", "editor-token", nil, http.StatusForbidden},
		{"member cannot publish", http.MethodPost, "/documents/1/publish", "member-token", nil, http.StatusForbidden},
		{"member cannot list users", http.MethodGet, "/admin/users", "member-token", nil, http.StatusForbidden},
		{"editor cannot read audit", http.MethodGet, "/admin/audit", "editor-token", nil, http.StatusForbidden},
		{"member cannot edit another's document", http.MethodPut, "/documents/3", "member-token",
			map[string]string{"title": "hijacked", "body": ""}, http.StatusForbidden},
		{"member cannot read another's draft", http.MethodGet, "/documents/3", "member-token", nil, http.StatusForbidden},

		// Allowed paths.
		{"member creates", http.MethodPost, "/documents", "member-token",
			map[string]string{"title": "mine", "body": "hello"}, http.StatusCreated},
		{"member edits own", http.MethodPut, "/documents/2", "member-token",
			map[string]string{"title": "edited", "body": "hello"}, http.StatusOK},
		{"editor edits another's", http.MethodPut, "/documents/3", "editor-token",
			map[string]string{"title": "reviewed", "body": "hello"}, http.StatusOK},
		{"editor publishes", http.MethodPost, "/documents/3/publish", "editor-token", nil, http.StatusOK},
		{"admin lists users", http.MethodGet, "/admin/users", "admin-token", nil, http.StatusOK},
		{"admin deletes", http.MethodDelete, "/documents/1", "admin-token", nil, http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(t, server, test.method, test.path, test.token, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}

			// A 403 body must not explain the policy to the caller.
			if status == http.StatusForbidden && !bytes.Contains(body, []byte(`"forbidden"`)) {
				t.Fatalf("403 body leaks detail: %s", body)
			}
		})
	}
}

func TestUnauthorizedCarriesChallenge(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/documents", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("401 without a WWW-Authenticate challenge")
	}
}

// TestListEndpointRespectsReadPolicy guards against the most common data leak
// in RBAC systems: a list endpoint that skips the per-item check.
func TestListEndpointRespectsReadPolicy(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	status, body := call(t, server, http.MethodGet, "/documents", "member-token", nil)

	if status != http.StatusOK {
		t.Fatalf("status = %d (%s)", status, body)
	}

	var response struct {
		Documents []Document `json:"documents"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, document := range response.Documents {
		if !document.Published && document.OwnerID != 2 {
			t.Fatalf("member can see another user's draft in the list: %+v", document)
		}
	}
}

// TestOwnerCannotBeSpoofed covers the create path: ownership comes from the
// token, not from the request body.
func TestOwnerCannotBeSpoofed(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	// owner_id is not an accepted field, so the request is rejected outright.
	status, _ := call(t, server, http.MethodPost, "/documents", "member-token",
		map[string]any{"title": "spoofed", "body": "x", "owner_id": 5})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown field", status)
	}

	status, body := call(t, server, http.MethodPost, "/documents", "member-token",
		map[string]any{"title": "legit", "body": "x"})

	if status != http.StatusCreated {
		t.Fatalf("status = %d (%s)", status, body)
	}

	var created Document

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if created.OwnerID != 2 {
		t.Fatalf("owner = %d, want the authenticated user (2)", created.OwnerID)
	}
}
