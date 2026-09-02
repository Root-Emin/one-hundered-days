package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/domain"
)

func TestGenerateProducesADistinctPrefixedKey(t *testing.T) {
	seen := make(map[string]bool)

	for i := 0; i < 50; i++ {
		generated, err := auth.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}

		if !strings.HasPrefix(generated.Plaintext, auth.Prefix) {
			t.Errorf("key %q has no %q prefix; a secret scanner cannot spot it", generated.Plaintext, auth.Prefix)
		}

		if generated.Hash == generated.Plaintext {
			t.Fatal("the hash equals the plaintext")
		}

		if generated.Hash != auth.Hash(generated.Plaintext) {
			t.Error("the stored hash does not match the plaintext's hash")
		}

		if seen[generated.Plaintext] {
			t.Fatal("duplicate key generated")
		}

		seen[generated.Plaintext] = true
	}
}

func TestHashIsStable(t *testing.T) {
	if auth.Hash("lk_example") != auth.Hash("lk_example") {
		t.Error("Hash is not deterministic")
	}

	if auth.Hash("lk_a") == auth.Hash("lk_b") {
		t.Error("different keys hashed to the same value")
	}
}

func TestEqual(t *testing.T) {
	if !auth.Equal("abc", "abc") {
		t.Error("Equal reported identical strings as different")
	}

	if auth.Equal("abc", "abd") || auth.Equal("abc", "abcd") {
		t.Error("Equal reported different strings as identical")
	}
}

func TestExtractKey(t *testing.T) {
	cases := map[string]struct {
		header string
		want   string
		fails  bool
	}{
		"bearer":           {header: "Bearer lk_abc", want: "lk_abc"},
		"lowercase scheme": {header: "bearer lk_abc", want: "lk_abc"},
		"missing":          {header: "", fails: true},
		"wrong scheme":     {header: "Basic dXNlcjpwYXNz", fails: true},
		"no credential":    {header: "Bearer", fails: true},
		"empty credential": {header: "Bearer   ", fails: true},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/links", nil)

			if testCase.header != "" {
				request.Header.Set("Authorization", testCase.header)
			}

			key, err := auth.ExtractKey(request)

			if testCase.fails {
				if !errors.Is(err, domain.ErrUnauthorized) {
					t.Errorf("error = %v, want ErrUnauthorized", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ExtractKey: %v", err)
			}

			if key != testCase.want {
				t.Errorf("key = %q, want %q", key, testCase.want)
			}
		})
	}
}

// A key in a query parameter ends up in access logs, Referer headers and
// browser history, so it is not accepted at all.
func TestQueryParameterIsNotACredential(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/links?api_key=lk_abc", nil)

	if _, err := auth.ExtractKey(request); err == nil {
		t.Error("a key in the query string was accepted")
	}
}

type stubResolver struct {
	owner string
	err   error
	seen  string
}

func (s *stubResolver) OwnerForHash(_ context.Context, hash string) (string, error) {
	s.seen = hash

	if s.err != nil {
		return "", s.err
	}

	return s.owner, nil
}

func TestMiddlewareAttachesTheOwner(t *testing.T) {
	resolver := &stubResolver{owner: "ada"}

	var seen string

	handler := auth.Middleware(resolver, failOnError(t))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = auth.OwnerFrom(r.Context())
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/links", nil)
	request.Header.Set("Authorization", "Bearer lk_secret")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "ada" {
		t.Errorf("owner = %q, want ada", seen)
	}

	// The plaintext never reaches the resolver, so it cannot reach a query log.
	if resolver.seen == "lk_secret" {
		t.Error("the plaintext key was passed to the resolver")
	}

	if resolver.seen != auth.Hash("lk_secret") {
		t.Errorf("resolver saw %q, want the hash", resolver.seen)
	}
}

// A middleware that forwards an unauthenticated request "for the handler to
// check" is how an endpoint ends up unprotected after a refactor.
func TestMiddlewareStopsUnauthenticatedRequests(t *testing.T) {
	resolver := &stubResolver{err: domain.ErrUnauthorized}

	reached := false

	var captured error

	handler := auth.Middleware(resolver, func(w http.ResponseWriter, _ *http.Request, err error) {
		captured = err

		w.WriteHeader(http.StatusUnauthorized)
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/links", nil)
	request.Header.Set("Authorization", "Bearer lk_wrong")

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if reached {
		t.Error("the handler ran for an unauthenticated request")
	}

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}

	if !errors.Is(captured, domain.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", captured)
	}
}

// The context key is unexported, so nothing outside the package can forge an
// identity by writing to the same key.
func TestOwnerFromRejectsAnEmptyOwner(t *testing.T) {
	if _, ok := auth.OwnerFrom(context.Background()); ok {
		t.Error("an owner was found in an empty context")
	}

	if _, ok := auth.OwnerFrom(auth.WithOwner(context.Background(), "")); ok {
		t.Error("an empty owner was reported as authenticated")
	}

	owner, ok := auth.OwnerFrom(auth.WithOwner(context.Background(), "ada"))

	if !ok || owner != "ada" {
		t.Errorf("owner = %q, ok = %t", owner, ok)
	}
}

func failOnError(t *testing.T) func(http.ResponseWriter, *http.Request, error) {
	t.Helper()

	return func(_ http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("unexpected auth error: %v", err)
	}
}
