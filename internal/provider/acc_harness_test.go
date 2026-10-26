package provider

// Acceptance test harness: when TF_ACC is set, TestMain starts a real `porch serve` in development mode (free ports,
// temporary data directory), completes the first-run setup, issues automation tokens and points the provider at it.
// When the fake Google IdP binary is available it also signs in a non-operator user who becomes a delegated
// administrator, so privilege-escalation checks can be exercised end to end.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

// accEnv describes the Porch instance the acceptance tests run against.
type accEnv struct {
	Endpoint     string // public URL (http://localhost:PORT)
	AdminToken   string // installation admin token (never expires, like an owner's Terraform token)
	AuditorToken string // installation auditor token (read-only)
	// Delegated is a tenant token of a non-operator member who holds only role:manage in DelegatedTenant ("" when the
	// fake IdP is unavailable).
	DelegatedToken  string
	DelegatedTenant string
	DelegatedUserID string
	// LimitedToken is a tenant token (created by the owner) holding only group:manage in LimitedTenant.
	LimitedToken  string
	LimitedTenant string
	logDir        string
}

var acc *accEnv

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"porch": providerserver.NewProtocol6WithError(New("test")()),
}

func TestMain(m *testing.M) {
	// Use a locally installed Terraform CLI so nothing is downloaded.
	if os.Getenv("TF_ACC_TERRAFORM_PATH") == "" {
		if p, err := exec.LookPath("terraform"); err == nil {
			_ = os.Setenv("TF_ACC_TERRAFORM_PATH", p)
		} else if _, err := os.Stat("/opt/homebrew/bin/terraform"); err == nil {
			_ = os.Setenv("TF_ACC_TERRAFORM_PATH", "/opt/homebrew/bin/terraform")
		}
	}
	_ = os.Setenv("CHECKPOINT_DISABLE", "1")
	if os.Getenv("TF_ACC") == "" {
		os.Exit(m.Run())
	}
	env, stop, err := startPorch()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance harness:", err)
		stop()
		os.Exit(1)
	}
	acc = env
	_ = os.Setenv(EnvEndpoint, env.Endpoint)
	_ = os.Setenv(EnvToken, env.AdminToken)
	code := m.Run()
	if code != 0 {
		dumpLog(filepath.Join(env.logDir, "porch.log"))
	}
	stop()
	os.Exit(code)
}

func dumpLog(p string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	if len(b) > 8000 {
		b = b[len(b)-8000:]
	}
	fmt.Fprintf(os.Stderr, "--- tail of %s\n%s\n", p, b)
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func binary(env, name string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	return filepath.Join(repoRoot(), "bin", name)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startPorch launches Porch (and the fake IdP) and returns the environment and a stop function.
func startPorch() (*accEnv, func(), error) {
	var procs []*exec.Cmd
	work, err := os.MkdirTemp("", "porch-tfacc-")
	stop := func() {
		for _, p := range procs {
			if p.Process != nil {
				_ = p.Process.Kill()
				_, _ = p.Process.Wait()
			}
		}
		if work != "" && os.Getenv("PORCH_ACC_KEEP") == "" {
			_ = os.RemoveAll(work)
		}
	}
	if err != nil {
		return nil, stop, err
	}
	if err := os.Chmod(work, 0o700); err != nil {
		return nil, stop, err
	}
	porchBin := binary("PORCH_BINARY", "porch")
	if _, err := os.Stat(porchBin); err != nil {
		return nil, stop, fmt.Errorf("porch binary not found at %s (run `make porch-bin` or set PORCH_BINARY)", porchBin)
	}
	pub, err := freePort()
	if err != nil {
		return nil, stop, err
	}
	mgmt, err := freePort()
	if err != nil {
		return nil, stop, err
	}
	idpPort, err := freePort()
	if err != nil {
		return nil, stop, err
	}
	origin := fmt.Sprintf("http://localhost:%d", pub)
	cfgPath := filepath.Join(work, "porch.yaml")
	cfg := fmt.Sprintf(`mode: development
data_dir: %s
public_url: %s
http:
  listen: 127.0.0.1:%d
management:
  listen: 127.0.0.1:%d
outbound:
  allow_cidrs: [127.0.0.1/32]
log:
  format: text
  level: warn
`, filepath.Join(work, "data"), origin, pub, mgmt)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return nil, stop, err
	}
	// Porch refuses unknown PORCH_* environment variables (the provider's own PORCH_ENDPOINT/PORCH_TOKEN included).
	childEnv := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PORCH_") {
			childEnv = append(childEnv, kv)
		}
	}
	setupCmd := exec.Command(porchBin, "admin", "setup-token", "-config", cfgPath) //nolint:gosec // test binary
	setupCmd.Env = childEnv
	out, err := setupCmd.CombinedOutput()
	if err != nil {
		return nil, stop, fmt.Errorf("setup-token: %w: %s", err, out)
	}
	setupToken := regexp.MustCompile(`porch_setup_[A-Za-z0-9_-]+`).FindString(string(out))
	if setupToken == "" {
		return nil, stop, errors.New("setup-token printed no token")
	}
	logf, err := os.Create(filepath.Join(work, "porch.log"))
	if err != nil {
		return nil, stop, err
	}
	srv := exec.Command(porchBin, "serve", "-config", cfgPath) //nolint:gosec // test binary
	srv.Stdout, srv.Stderr, srv.Env = logf, logf, childEnv
	if err := srv.Start(); err != nil {
		return nil, stop, err
	}
	procs = append(procs, srv)

	fakeBin := binary("PORCH_FAKEGOOGLE", "fakegoogle")
	haveIdP := false
	if _, err := os.Stat(fakeBin); err == nil {
		idpLog, _ := os.Create(filepath.Join(work, "idp.log"))
		idp := exec.Command(fakeBin, "-listen", fmt.Sprintf("127.0.0.1:%d", idpPort), "-issuer", fmt.Sprintf("http://127.0.0.1:%d", idpPort), //nolint:gosec
			"-client-id", "porch", "-client-secret", "porch-acc-secret", "-redirect-uris", origin+"/login/fake/callback", "-auto-login", "ada")
		idp.Stdout, idp.Stderr, idp.Env = idpLog, idpLog, childEnv
		if err := idp.Start(); err == nil {
			procs = append(procs, idp)
			haveIdP = true
		}
	}

	ready := false
	for range 100 {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", mgmt))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		dumpLog(filepath.Join(work, "porch.log"))
		return nil, stop, errors.New("porch did not become ready")
	}

	owner := &browser{origin: origin, cookies: map[string]string{}}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	if err := owner.json(http.MethodPost, "/api/v1/setup", map[string]string{"token": setupToken, "display_name": "Owner"}, &me); err != nil {
		return nil, stop, fmt.Errorf("setup: %w", err)
	}
	owner.csrf = me.CSRF
	env := &accEnv{Endpoint: origin, logDir: work}
	var tok struct {
		Token string `json:"token"`
	}
	if err := owner.json(http.MethodPost, "/api/v1/installation/automation-tokens",
		map[string]any{"name": "tf-acc", "role": "admin", "never_expires": true}, &tok); err != nil {
		return nil, stop, fmt.Errorf("admin token: %w", err)
	}
	env.AdminToken = tok.Token
	if err := owner.json(http.MethodPost, "/api/v1/installation/automation-tokens",
		map[string]any{"name": "tf-acc-auditor", "role": "auditor", "expires_in_days": 1}, &tok); err != nil {
		return nil, stop, fmt.Errorf("auditor token: %w", err)
	}
	env.AuditorToken = tok.Token

	admin, err := client.New(client.Config{Endpoint: origin, Token: env.AdminToken, UserAgent: "tfacc-harness"})
	if err != nil {
		return nil, stop, err
	}
	ctx := context.Background()
	limited, err := admin.CreateTenant(ctx, client.TenantCreate{Slug: "acc-limited", Name: "Limited"})
	if err != nil {
		return nil, stop, err
	}
	env.LimitedTenant = limited.ID
	if err := owner.json(http.MethodPost, "/api/v1/tenants/acc-limited/automation-tokens",
		map[string]any{"name": "tf-acc-limited", "permissions": []string{"group:manage"}, "expires_in_days": 1}, &tok); err != nil {
		return nil, stop, fmt.Errorf("limited token: %w", err)
	}
	env.LimitedToken = tok.Token

	if haveIdP {
		for range 50 {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/.well-known/openid-configuration", idpPort))
			if err == nil {
				_ = resp.Body.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := setupDelegated(ctx, env, admin, origin, idpPort); err != nil {
			fmt.Fprintln(os.Stderr, "acceptance harness: delegated administrator unavailable:", err)
			dumpLog(filepath.Join(work, "idp.log"))
		}
	}
	return env, stop, nil
}

// setupDelegated signs "ada" in through the fake IdP (JIT enrollment into a tenant), grants her role:manage and lets
// her issue a tenant token (she is not an installation operator, so the escalation rule applies to her).
func setupDelegated(ctx context.Context, env *accEnv, admin *client.Client, origin string, idpPort int) error {
	enabled := true
	prov, err := admin.CreateIdentityProvider(ctx, client.IdentityProviderCreate{Slug: "fake", Kind: "oidc", Name: "Fake IdP",
		Issuer: fmt.Sprintf("http://127.0.0.1:%d", idpPort), ClientID: "porch", ClientSecret: "porch-acc-secret", Enabled: &enabled})
	if err != nil {
		return err
	}
	t, err := admin.CreateTenant(ctx, client.TenantCreate{Slug: "acc-delegated", Name: "Delegated"})
	if err != nil {
		return err
	}
	if _, err := admin.CreateDomainRule(ctx, t.ID, client.DomainRuleCreate{Domain: "smith.example", ProviderID: prov.ID,
		EvidenceKind: "verified_email", Mode: "jit"}); err != nil {
		return err
	}
	ada := &browser{origin: origin, cookies: map[string]string{}}
	if err := ada.login("/login/fake"); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	u, err := admin.FindUserByEmail(ctx, "ada@smith.example")
	if err != nil {
		return err
	}
	role, err := admin.CreateRole(ctx, t.ID, client.RoleRequest{Slug: "role-manager", Name: "Role manager", Permissions: []string{"role:manage"}})
	if err != nil {
		return err
	}
	if _, err := admin.CreateAssignment(ctx, t.ID, client.AssignmentCreate{SubjectType: "user", SubjectID: u.ID, RoleID: role.ID,
		ScopeType: "tenant"}); err != nil {
		return err
	}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	if err := ada.json(http.MethodGet, "/api/v1/me", nil, &me); err != nil {
		return err
	}
	ada.csrf = me.CSRF
	var tok struct {
		Token string `json:"token"`
	}
	if err := ada.json(http.MethodPost, "/api/v1/tenants/"+t.ID+"/automation-tokens",
		map[string]any{"name": "ada-delegated", "permissions": []string{"role:manage"}, "expires_in_days": 1}, &tok); err != nil {
		return fmt.Errorf("delegated token: %w", err)
	}
	env.DelegatedToken, env.DelegatedTenant, env.DelegatedUserID = tok.Token, t.ID, u.ID
	return nil
}

// browser is a minimal cookie-aware client. `__Host-` cookies carry the Secure attribute, which Go's cookie jar
// refuses to send over http://localhost, so cookies are tracked by hand.
type browser struct {
	origin  string
	csrf    string
	cookies map[string]string
}

func (b *browser) do(method, target string, body any) (*http.Response, []byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	u := target
	if strings.HasPrefix(target, "/") {
		u = b.origin + target
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.HasPrefix(u, b.origin) {
		req.Header.Set("Origin", b.origin)
		if b.csrf != "" {
			req.Header.Set("X-Porch-CSRF", b.csrf)
		}
		var cs []string
		for k, v := range b.cookies {
			cs = append(cs, k+"="+v)
		}
		if len(cs) > 0 {
			req.Header.Set("Cookie", strings.Join(cs, "; "))
		}
	}
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if strings.HasPrefix(u, b.origin) {
		for _, c := range resp.Cookies() {
			if c.MaxAge < 0 || c.Value == "" {
				delete(b.cookies, c.Name)
			} else {
				b.cookies[c.Name] = c.Value
			}
		}
	}
	return resp, data, nil
}

func (b *browser) json(method, p string, body, out any) error {
	resp, data, err := b.do(method, p, body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, p, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// login follows the upstream login redirects until Porch sets a session cookie.
func (b *browser) login(start string) error {
	next := start
	for range 10 {
		resp, _, err := b.do(http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
			break
		}
		loc, err := resp.Location()
		if err != nil {
			return err
		}
		if loc.Query().Get("error") != "" {
			return fmt.Errorf("login failed: %s", loc.Query().Get("error"))
		}
		next = loc.String()
		if pu, _ := url.Parse(b.origin); loc.Host == pu.Host && loc.Path == "/" {
			break
		}
	}
	for name := range b.cookies {
		if strings.Contains(name, "porch_session") {
			return nil
		}
	}
	return errors.New("no session cookie after login")
}

// testAccPreCheck guards acceptance tests.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests need TF_ACC=1")
	}
	if acc == nil {
		t.Fatal("acceptance environment not initialised (TF_ACC must be set)")
	}
}

// adminClient is an out-of-band API client for drift and destroy checks.
func adminClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Endpoint: acc.Endpoint, Token: acc.AdminToken, UserAgent: "tfacc-oob"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
