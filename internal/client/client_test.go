package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "porch_at_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"

func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL, Token: testToken, UserAgent: "terraform-provider-porch/test", MaxRetries: 3,
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c, srv
}

func writeJSON(w http.ResponseWriter, status int, etag string, v any) {
	w.Header().Set("Content-Type", "application/json")
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string, fields []FieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status,
		"detail": detail, "code": code, "fields": fields})
}

// dropConnection closes the TCP connection without a response, simulating a network failure after the server
// processed the request.
func dropConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("no hijacker")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestNewValidatesConfig(t *testing.T) {
	cases := []struct {
		endpoint, token, wantErr string
	}{
		{"https://auth.example.com", "porch_setup_x", "porch_at_"},
		{"https://auth.example.com", "porch_at_", "malformed"},
		{"https://auth.example.com", "porch_at_abc def", "malformed"},
		{"auth.example.com", testToken, "absolute"},
		{"ftp://auth.example.com", testToken, "absolute"},
		{"https://user:pw@auth.example.com", testToken, "credentials"},
		{"https://auth.example.com?x=1", testToken, "query"},
	}
	for _, tc := range cases {
		_, err := New(Config{Endpoint: tc.endpoint, Token: tc.token})
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("New(%q, %q) = %v, want error containing %q", tc.endpoint, tc.token, err, tc.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), tc.token) && tc.token != "porch_at_" {
			t.Errorf("error leaks the token: %v", err)
		}
	}
	if _, err := New(Config{Endpoint: "https://auth.example.com", Token: testToken, CAFile: "/nonexistent/ca.pem"}); err == nil {
		t.Error("missing CA file accepted")
	}
}

func TestParseEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"https://auth.example.com":               "https://auth.example.com/api/v1",
		"https://auth.example.com/":              "https://auth.example.com/api/v1",
		"https://auth.example.com/api/v1":        "https://auth.example.com/api/v1",
		"https://auth.example.com/porch/":        "https://auth.example.com/porch/api/v1",
		"http://localhost:8080/porch/api/v1/":    "http://localhost:8080/porch/api/v1",
		"  https://auth.example.com:8443/api/v1": "https://auth.example.com:8443/api/v1",
	} {
		u, err := ParseEndpoint(in)
		if err != nil || u.String() != want {
			t.Errorf("ParseEndpoint(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
}

func TestHeadersETagAndIfMatch(t *testing.T) {
	var seen []http.Header
	var mu sync.Mutex
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tenants":
			writeJSON(w, http.StatusCreated, `"1"`, Tenant{ID: "ten_1", Slug: "a", Name: "A", Status: "active", Version: 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tenants/ten_1":
			writeJSON(w, http.StatusOK, `"1"`, Tenant{ID: "ten_1", Slug: "a", Name: "A", Status: "active", Version: 1})
		case r.Method == http.MethodPatch:
			if r.Header.Get("If-Match") != `"1"` {
				writeProblem(w, http.StatusPreconditionFailed, "precondition_failed", "resource was changed", nil)
				return
			}
			writeJSON(w, http.StatusOK, `"2"`, Tenant{ID: "ten_1", Slug: "a", Name: "B", Status: "active", Version: 2})
		case r.Method == http.MethodDelete:
			if r.Header.Get("If-Match") != `"2"` {
				writeProblem(w, http.StatusPreconditionFailed, "precondition_failed", "resource was changed", nil)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	ctx := context.Background()
	tn, err := c.CreateTenant(ctx, TenantCreate{Slug: "a", Name: "A"})
	if err != nil || tn.ETag != `"1"` {
		t.Fatalf("create: %+v %v", tn, err)
	}
	if _, err := c.GetTenant(ctx, "ten_1"); err != nil {
		t.Fatal(err)
	}
	name := "B"
	if _, err := c.UpdateTenant(ctx, "ten_1", `"9"`, TenantUpdate{Name: &name}); !HasCode(err, "precondition_failed") {
		t.Fatalf("stale If-Match: %v", err)
	}
	up, err := c.UpdateTenant(ctx, "ten_1", tn.ETag, TenantUpdate{Name: &name})
	if err != nil || up.ETag != `"2"` || up.Name != "B" {
		t.Fatalf("update: %+v %v", up, err)
	}
	if err := c.DeleteTenant(ctx, "ten_1", up.ETag); err != nil {
		t.Fatal(err)
	}
	for i, h := range seen {
		if h.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("request %d: missing bearer token", i)
		}
		if h.Get("User-Agent") != "terraform-provider-porch/test" {
			t.Errorf("request %d: user agent %q", i, h.Get("User-Agent"))
		}
		if h.Get("X-Porch-CSRF") != "" || h.Get("Cookie") != "" {
			t.Errorf("request %d: ambient credentials sent", i)
		}
	}
	if seen[0].Get("Idempotency-Key") == "" || seen[0].Get("X-Porch-Managed-By") != "terraform" {
		t.Errorf("create headers: %v", seen[0])
	}
	if seen[1].Get("X-Porch-Managed-By") != "" || seen[1].Get("Idempotency-Key") != "" {
		t.Errorf("GET must not carry mutation headers: %v", seen[1])
	}
	for _, i := range []int{2, 3, 4} {
		if seen[i].Get("X-Porch-Managed-By") != "terraform" || seen[i].Get("Idempotency-Key") != "" {
			t.Errorf("request %d headers: %v", i, seen[i])
		}
	}
}

func TestETagFallsBackToVersion(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Idempotent replays carry no ETag header.
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusCreated, "", Group{ID: "grp_1", Slug: "g", Name: "G", Version: 7})
	}))
	g, err := c.CreateGroup(context.Background(), "t", GroupCreate{Slug: "g", Name: "G"})
	if err != nil || g.ETag != `"7"` {
		t.Fatalf("got %+v, %v", g, err)
	}
}

func TestCreateRetriesNetworkErrorWithSameIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	var keys []string
	var mu sync.Mutex
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"slug":"g"`) {
			t.Errorf("body not resent: %s", body)
		}
		switch calls.Add(1) {
		case 1:
			dropConnection(t, w)
		case 2:
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "try again later", nil)
		case 3:
			writeProblem(w, http.StatusConflict, "idempotency_in_progress", "retry shortly", nil)
		default:
			writeJSON(w, http.StatusCreated, `"1"`, Group{ID: "grp_1", Slug: "g", Name: "G", Version: 1})
		}
	}))
	g, err := c.CreateGroup(context.Background(), "t", GroupCreate{Slug: "g", Name: "G"})
	if err != nil || g.ID != "grp_1" {
		t.Fatalf("got %+v, %v", g, err)
	}
	if len(keys) != 4 {
		t.Fatalf("attempts = %d, want 4", len(keys))
	}
	for _, k := range keys {
		if k == "" || k != keys[0] {
			t.Fatalf("idempotency keys differ across retries: %v", keys)
		}
	}
	// A second logical create uses a fresh key.
	calls.Store(10)
	if _, err := c.CreateGroup(context.Background(), "t", GroupCreate{Slug: "g", Name: "G"}); err != nil {
		t.Fatal(err)
	}
	if keys[len(keys)-1] == keys[0] {
		t.Error("idempotency key reused for a new logical create")
	}
}

func TestSecretWithheldReadsLocation(t *testing.T) {
	var posts atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			if posts.Add(1) == 1 {
				dropConnection(t, w) // the server created the app, the response was lost
				return
			}
			w.Header().Set("Location", "/api/v1/tenants/fam/applications/app_1")
			writeProblem(w, http.StatusConflict, "idempotent_secret_withheld", "the original request succeeded", nil)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tenants/fam/applications/app_1":
			writeJSON(w, http.StatusOK, `"2"`, Application{ID: "app_1", Slug: "a", Kind: "oidc", Version: 2,
				Client: &OIDCClient{ClientID: "cli_1", ClientType: "confidential"}})
		default:
			http.NotFound(w, r)
		}
	}))
	a, created, err := c.CreateApplication(context.Background(), "fam", ApplicationCreate{Slug: "a", Name: "A", Kind: "oidc"})
	if err != nil {
		t.Fatal(err)
	}
	if !created.SecretWithheld || a.ID != "app_1" || a.ClientSecret != "" || a.ETag != `"2"` {
		t.Fatalf("got %+v %+v", a, created)
	}
}

func TestSecretWithheldRejectsForeignLocation(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example.com/api/v1/x")
		writeProblem(w, http.StatusConflict, "idempotent_secret_withheld", "withheld", nil)
	}))
	if _, _, err := c.CreateServiceAccount(context.Background(), "t", ServiceAccountCreate{Slug: "s", Name: "S"}); err == nil {
		t.Fatal("followed a foreign Location")
	}
}

func TestConditionalWritesAreNotRetriedOnAmbiguousFailures(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeProblem(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}))
	name := "x"
	if _, err := c.UpdateUser(context.Background(), "usr_1", `"1"`, UserUpdate{DisplayName: &name}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("PATCH retried %d times on 500", calls.Load())
	}
	calls.Store(0)
	if _, err := c.ReplaceRole(context.Background(), "t", "rol_1", `"1"`, RoleRequest{Name: "r"}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("PUT retried %d times on 500", calls.Load())
	}
	calls.Store(0)
	if _, err := c.GetUser(context.Background(), "usr_1"); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 4 {
		t.Fatalf("GET attempts = %d, want 4", calls.Load())
	}
}

func TestRetryOn429And503ForPatch(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			writeProblem(w, http.StatusTooManyRequests, "rate_limited", "slow down", nil)
		case 2:
			writeProblem(w, http.StatusServiceUnavailable, "unavailable", "try again later", nil)
		default:
			writeJSON(w, http.StatusOK, `"3"`, User{ID: "usr_1", DisplayName: "x", Version: 3})
		}
	}))
	name := "x"
	u, err := c.UpdateUser(context.Background(), "usr_1", `"2"`, UserUpdate{DisplayName: &name})
	if err != nil || u.ETag != `"3"` {
		t.Fatalf("got %+v %v", u, err)
	}
}

func TestProblemDecoding(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tenants/fam/roles":
			writeProblem(w, http.StatusForbidden, "privilege_escalation", "you cannot grant permissions you do not hold: app:manage",
				[]FieldError{{Field: "permissions", Message: "not held: app:manage"}})
		case "/api/v1/tenants/fam/groups":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"status":422,"title":"Unprocessable Entity","detail":"validation failed","code":"validation_failed",` +
				`"errors":[{"message":"expected length >= 1","location":"body.name","value":""}]}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>secret-ish proxy page</html>"))
		}
	}))
	_, err := c.CreateRole(context.Background(), "fam", RoleRequest{Slug: "r", Name: "R", Permissions: []string{"app:manage"}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusForbidden || ae.Code != "privilege_escalation" || len(ae.Fields) != 1 {
		t.Fatalf("got %#v", err)
	}
	msg := err.Error()
	for _, want := range []string{"403", "privilege_escalation", "you cannot grant permissions you do not hold: app:manage", "permissions: not held"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	_, err = c.CreateGroup(context.Background(), "fam", GroupCreate{Slug: "g"})
	if !HasCode(err, "validation_failed") || !strings.Contains(err.Error(), "body.name: expected length >= 1") {
		t.Fatalf("got %v", err)
	}
	c.maxRetries = 0
	_, err = c.GetTenant(context.Background(), "x")
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusBadGateway || strings.Contains(err.Error(), "proxy page") {
		t.Fatalf("got %v", err)
	}
}

func TestNotFoundHandling(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tenants/fam/members":
			// Two pages; the membership is not there.
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, http.StatusOK, "", map[string]any{"items": []Member{{ID: "mem_1", UserID: "usr_1", Version: 1}}, "next_cursor": "c2"})
				return
			}
			writeJSON(w, http.StatusOK, "", map[string]any{"items": []Member{{ID: "mem_2", UserID: "usr_2", Version: 4}}})
		default:
			writeProblem(w, http.StatusNotFound, "not_found", "application not found", nil)
		}
	}))
	ctx := context.Background()
	if _, err := c.GetApplication(ctx, "fam", "app_x"); !IsNotFound(err) {
		t.Fatalf("got %v", err)
	}
	if _, err := c.GetMember(ctx, "fam", "mem_x"); !IsNotFound(err) {
		t.Fatalf("got %v", err)
	}
	m, err := c.GetMember(ctx, "fam", "mem_2")
	if err != nil || m.UserID != "usr_2" || m.ETag != `"4"` {
		t.Fatalf("paginated lookup: %+v %v", m, err)
	}
	if err := c.DeleteApplication(ctx, "fam", "app_x", `"1"`); !IsNotFound(err) {
		t.Fatalf("delete: %v", err)
	}
}

func TestAddGroupMemberIsIdempotent(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			writeProblem(w, http.StatusUnprocessableEntity, "validation_failed", "user must be a member of this tenant (or the user is already in it)", nil)
		case http.MethodGet:
			writeJSON(w, http.StatusOK, `"1"`, Group{ID: "grp_1", Members: []string{"usr_1"}, Parents: []string{}})
		}
	}))
	if err := c.AddGroupMember(context.Background(), "t", "grp_1", "usr_1"); err != nil {
		t.Fatalf("already a member: %v", err)
	}
	if err := c.AddGroupMember(context.Background(), "t", "grp_1", "usr_2"); !HasCode(err, "validation_failed") {
		t.Fatalf("non-member: %v", err)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeProblem(w, http.StatusServiceUnavailable, "unavailable", "down", nil)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	c.Sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	if _, err := c.GetTenant(ctx, "x"); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("attempts after cancel: %d", calls.Load())
	}
}

func TestBackoffBounds(t *testing.T) {
	c := &Client{minBackoff: 100 * time.Millisecond, maxBackoff: time.Second}
	for attempt := range 10 {
		d := c.backoff(attempt, nil)
		if d <= 0 || d > time.Second {
			t.Fatalf("attempt %d: backoff %s out of bounds", attempt, d)
		}
	}
	h := http.Header{}
	h.Set("Retry-After", "30")
	if d := c.backoff(0, h); d != time.Second {
		t.Fatalf("Retry-After not capped: %s", d)
	}
}

func TestObserverNeverSeesBodies(t *testing.T) {
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, `"1"`, ServiceAccount{ID: "sva_1", Version: 1, ClientSecret: "porch_sa_supersecret"})
	}))
	var logged []string
	c.observer = func(_ context.Context, method, path string, status, attempt int, err error) {
		logged = append(logged, method+" "+path)
	}
	sa, created, err := c.CreateServiceAccount(context.Background(), "t", ServiceAccountCreate{Slug: "s", Name: "S"})
	if err != nil || sa.ClientSecret != "porch_sa_supersecret" || created.SecretWithheld {
		t.Fatalf("got %+v %v", sa, err)
	}
	for _, l := range logged {
		if strings.Contains(l, "secret") || strings.Contains(l, testToken) {
			t.Fatalf("observer saw sensitive data: %s", l)
		}
	}
	if c.Endpoint() != srv.URL {
		t.Fatalf("Endpoint() = %s, want %s", c.Endpoint(), srv.URL)
	}
}
