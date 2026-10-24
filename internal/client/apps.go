package client

import (
	"context"
	"net/http"
	"time"
)

// Disclosure controls optional claims released to an application.
type Disclosure struct {
	Email        bool `json:"email"`
	Groups       bool `json:"groups"`
	Roles        bool `json:"roles"`
	Entitlements bool `json:"entitlements"`
}

// OIDCClient is an application's OAuth/OIDC client as returned by the API.
type OIDCClient struct {
	ClientID        string   `json:"client_id"`
	ClientType      string   `json:"client_type"`
	ApplicationType string   `json:"application_type"`
	SecretHint      string   `json:"secret_hint,omitempty"`
	RedirectURIs    []string `json:"redirect_uris"`
	PostLogoutURIs  []string `json:"post_logout_redirect_uris"`
	GrantTypes      []string `json:"grant_types"`
	AllowedScopes   []string `json:"allowed_scopes"`
	Audiences       []string `json:"audiences"`
	AllowedOrigins  []string `json:"allowed_origins"`
	RequirePKCE     bool     `json:"require_pkce"`
	SigningAlg      string   `json:"signing_alg"`
	AccessTokenTTL  int64    `json:"access_token_ttl_seconds"`
	IDTokenTTL      int64    `json:"id_token_ttl_seconds"`
	RefreshTTL      int64    `json:"refresh_ttl_seconds"`
	RefreshIdle     int64    `json:"refresh_idle_seconds"`
	Issuer          string   `json:"issuer"`
	DiscoveryURL    string   `json:"discovery_url"`
}

// ClientConfig configures an OIDC client (create, or full replacement on update).
type ClientConfig struct {
	ClientType      string   `json:"client_type,omitempty"`
	ApplicationType string   `json:"application_type,omitempty"`
	RedirectURIs    []string `json:"redirect_uris,omitempty"`
	PostLogoutURIs  []string `json:"post_logout_redirect_uris,omitempty"`
	GrantTypes      []string `json:"grant_types,omitempty"`
	AllowedScopes   []string `json:"allowed_scopes,omitempty"`
	Audiences       []string `json:"audiences,omitempty"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
	RequirePKCE     *bool    `json:"require_pkce,omitempty"`
	SigningAlg      string   `json:"signing_alg,omitempty"`
	AccessTokenTTL  int64    `json:"access_token_ttl_seconds,omitempty"`
	IDTokenTTL      int64    `json:"id_token_ttl_seconds,omitempty"`
	RefreshTTL      int64    `json:"refresh_ttl_seconds,omitempty"`
	RefreshIdle     int64    `json:"refresh_idle_seconds,omitempty"`
}

// Application is a tenant application.
type Application struct {
	ID             string            `json:"id"`
	TenantID       string            `json:"tenant_id"`
	Slug           string            `json:"slug"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Description    string            `json:"description"`
	LaunchURL      string            `json:"launch_url"`
	Enabled        bool              `json:"enabled"`
	Disclosure     Disclosure        `json:"disclosure"`
	AccessMode     string            `json:"access_mode"`
	Hosts          []string          `json:"hosts"`
	ForwardHeaders map[string]string `json:"forward_headers"`
	Client         *OIDCClient       `json:"client,omitempty"`
	Version        int64             `json:"version"`
	ETag           string            `json:"-"`
	// ClientSecret is only set on create (and never on a replayed create).
	ClientSecret string `json:"client_secret,omitempty"`
}

// ApplicationCreate creates an application.
type ApplicationCreate struct {
	Slug           string            `json:"slug"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Description    string            `json:"description,omitempty"`
	LaunchURL      string            `json:"launch_url,omitempty"`
	Disclosure     *Disclosure       `json:"disclosure,omitempty"`
	Client         *ClientConfig     `json:"client,omitempty"`
	AccessMode     string            `json:"access_mode,omitempty"`
	Hosts          []string          `json:"hosts,omitempty"`
	ForwardHeaders map[string]string `json:"forward_headers,omitempty"`
}

// ApplicationUpdate edits an application; nil fields are unchanged and client replaces the client configuration.
// Hosts and ForwardHeaders are pointers so that an empty value (clear) can be told apart from "unchanged".
type ApplicationUpdate struct {
	Name           *string            `json:"name,omitempty"`
	Description    *string            `json:"description,omitempty"`
	LaunchURL      *string            `json:"launch_url,omitempty"`
	Enabled        *bool              `json:"enabled,omitempty"`
	Disclosure     *Disclosure        `json:"disclosure,omitempty"`
	Client         *ClientConfig      `json:"client,omitempty"`
	AccessMode     *string            `json:"access_mode,omitempty"`
	Hosts          *[]string          `json:"hosts,omitempty"`
	ForwardHeaders *map[string]string `json:"forward_headers,omitempty"`
}

// ClientSecret is a newly issued secret.
type ClientSecret struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Created reports how a secret-bearing create went.
type Created struct {
	// SecretWithheld is true when the create succeeded on an earlier attempt whose response was lost: the secret was
	// not returned again (ADR-0041) and must be rotated to be known.
	SecretWithheld bool
}

func appsPath(tenant string) string { return "/tenants/" + esc(tenant) + "/applications" }

// CreateApplication creates an application. The client secret (confidential OIDC clients) is in ClientSecret.
func (c *Client) CreateApplication(ctx context.Context, tenant string, in ApplicationCreate) (*Application, Created, error) {
	a, cr, err := create[Application](ctx, c, appsPath(tenant), in)
	if err != nil {
		return nil, Created{}, err
	}
	a.ETag = etagFor(cr.resp, a.Version)
	return a, Created{SecretWithheld: cr.withheld}, nil
}

// GetApplication reads an application by ID.
func (c *Client) GetApplication(ctx context.Context, tenant, id string) (*Application, error) {
	a, resp, err := call[Application](ctx, c, &request{method: http.MethodGet, path: appsPath(tenant) + "/" + esc(id)})
	if err != nil {
		return nil, err
	}
	a.ETag = etagFor(resp, a.Version)
	a.ClientSecret = ""
	return a, nil
}

// UpdateApplication patches an application.
func (c *Client) UpdateApplication(ctx context.Context, tenant, id, etag string, in ApplicationUpdate) (*Application, error) {
	a, resp, err := call[Application](ctx, c, &request{method: http.MethodPatch, path: appsPath(tenant) + "/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	a.ETag = etagFor(resp, a.Version)
	return a, nil
}

// RotateApplicationSecret issues a new client secret; the old one stops working.
func (c *Client) RotateApplicationSecret(ctx context.Context, tenant, id string) (*ClientSecret, error) {
	s, _, err := call[ClientSecret](ctx, c, &request{method: http.MethodPost, path: appsPath(tenant) + "/" + esc(id) + "/rotate-secret"})
	return s, err
}

// DeleteApplication deletes an application.
func (c *Client) DeleteApplication(ctx context.Context, tenant, id, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: appsPath(tenant) + "/" + esc(id), ifMatch: etag})
	return err
}

// AppShare is a tenant an application is shared with.
type AppShare struct {
	TenantID   string    `json:"tenant_id"`
	TenantSlug string    `json:"tenant_slug"`
	TenantName string    `json:"tenant_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// ShareApplication shares an application with another tenant (ID or slug).
func (c *Client) ShareApplication(ctx context.Context, tenant, app, target string) error {
	_, err := c.do(ctx, &request{method: http.MethodPost, path: appsPath(tenant) + "/" + esc(app) + "/shares",
		body: map[string]string{"tenant": target}, create: true})
	return err
}

// GetApplicationShare finds the share of app with target (ID or slug).
func (c *Client) GetApplicationShare(ctx context.Context, tenant, app, target string) (*AppShare, error) {
	path := appsPath(tenant) + "/" + esc(app) + "/shares"
	ss, _, err := call[[]AppShare](ctx, c, &request{method: http.MethodGet, path: path})
	if err != nil {
		return nil, err
	}
	for _, s := range *ss {
		if s.TenantID == target || s.TenantSlug == target {
			return &s, nil
		}
	}
	return nil, notFound(path, "share with "+target)
}

// UnshareApplication stops sharing an application.
func (c *Client) UnshareApplication(ctx context.Context, tenant, app, target string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: appsPath(tenant) + "/" + esc(app) + "/shares/" + esc(target)})
	return err
}

// ServiceAccount is a tenant machine identity.
type ServiceAccount struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Enabled        bool     `json:"enabled"`
	SecretHint     string   `json:"secret_hint"`
	Audiences      []string `json:"audiences"`
	AccessTokenTTL int64    `json:"access_token_ttl_seconds"`
	TokenEndpoint  string   `json:"token_endpoint"`
	Version        int64    `json:"version"`
	ETag           string   `json:"-"`
	// ClientSecret is only set on create.
	ClientSecret string `json:"client_secret,omitempty"`
}

// ServiceAccountCreate creates a service account.
type ServiceAccountCreate struct {
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Audiences      []string `json:"audiences,omitempty"`
	AccessTokenTTL int64    `json:"access_token_ttl_seconds,omitempty"`
}

// ServiceAccountUpdate edits a service account; nil fields are unchanged (an empty Audiences clears them).
type ServiceAccountUpdate struct {
	Name           *string   `json:"name,omitempty"`
	Description    *string   `json:"description,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
	Audiences      *[]string `json:"audiences,omitempty"`
	AccessTokenTTL *int64    `json:"access_token_ttl_seconds,omitempty"`
}

func saPath(tenant string) string { return "/tenants/" + esc(tenant) + "/service-accounts" }

// CreateServiceAccount creates a service account; its secret is in ClientSecret.
func (c *Client) CreateServiceAccount(ctx context.Context, tenant string, in ServiceAccountCreate) (*ServiceAccount, Created, error) {
	a, cr, err := create[ServiceAccount](ctx, c, saPath(tenant), in)
	if err != nil {
		return nil, Created{}, err
	}
	a.ETag = etagFor(cr.resp, a.Version)
	return a, Created{SecretWithheld: cr.withheld}, nil
}

// GetServiceAccount reads a service account by ID or slug.
func (c *Client) GetServiceAccount(ctx context.Context, tenant, ref string) (*ServiceAccount, error) {
	a, resp, err := call[ServiceAccount](ctx, c, &request{method: http.MethodGet, path: saPath(tenant) + "/" + esc(ref)})
	if err != nil {
		return nil, err
	}
	a.ETag = etagFor(resp, a.Version)
	a.ClientSecret = ""
	return a, nil
}

// UpdateServiceAccount patches a service account.
func (c *Client) UpdateServiceAccount(ctx context.Context, tenant, id, etag string, in ServiceAccountUpdate) (*ServiceAccount, error) {
	a, resp, err := call[ServiceAccount](ctx, c, &request{method: http.MethodPatch, path: saPath(tenant) + "/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	a.ETag = etagFor(resp, a.Version)
	return a, nil
}

// RotateServiceAccountSecret issues a new secret; the old one stops working.
func (c *Client) RotateServiceAccountSecret(ctx context.Context, tenant, id string) (*ClientSecret, error) {
	s, _, err := call[ClientSecret](ctx, c, &request{method: http.MethodPost, path: saPath(tenant) + "/" + esc(id) + "/rotate-secret"})
	return s, err
}

// DeleteServiceAccount deletes a service account and its assignments.
func (c *Client) DeleteServiceAccount(ctx context.Context, tenant, id, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: saPath(tenant) + "/" + esc(id), ifMatch: etag})
	return err
}
