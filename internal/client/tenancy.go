package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Tenant is a Porch tenant.
type Tenant struct {
	ID                     string    `json:"id"`
	Slug                   string    `json:"slug"`
	Name                   string    `json:"name"`
	Status                 string    `json:"status"`
	SessionIdleSeconds     int64     `json:"session_idle_seconds,omitempty"`
	SessionAbsoluteSeconds int64     `json:"session_absolute_seconds,omitempty"`
	Version                int64     `json:"version"`
	ManagedBy              string    `json:"managed_by"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	ETag                   string    `json:"-"`
}

// TenantCreate creates a tenant.
type TenantCreate struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// TenantUpdate edits a tenant; nil fields are unchanged. Session values of 0 restore the installation default.
type TenantUpdate struct {
	Name                   *string `json:"name,omitempty"`
	Status                 *string `json:"status,omitempty"`
	SessionIdleSeconds     *int64  `json:"session_idle_seconds,omitempty"`
	SessionAbsoluteSeconds *int64  `json:"session_absolute_seconds,omitempty"`
}

type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// listAll follows cursor pagination.
func listAll[T any](ctx context.Context, c *Client, path string, q url.Values, stop func(T) bool) ([]T, error) {
	var out []T
	cursor := ""
	for range 10000 {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("limit", "200")
		if cursor != "" {
			qq.Set("cursor", cursor)
		}
		p, _, err := call[page[T]](ctx, c, &request{method: http.MethodGet, path: path, query: qq})
		if err != nil {
			return nil, err
		}
		for _, it := range p.Items {
			out = append(out, it)
			if stop != nil && stop(it) {
				return out, nil
			}
		}
		if p.NextCursor == "" || len(p.Items) == 0 {
			return out, nil
		}
		cursor = p.NextCursor
	}
	return out, nil
}

// CreateTenant creates a tenant.
func (c *Client) CreateTenant(ctx context.Context, in TenantCreate) (*Tenant, error) {
	t, cr, err := create[Tenant](ctx, c, "/tenants", in)
	if err != nil {
		return nil, err
	}
	t.ETag = etagFor(cr.resp, t.Version)
	return t, nil
}

// GetTenant reads a tenant by ID or slug.
func (c *Client) GetTenant(ctx context.Context, ref string) (*Tenant, error) {
	t, resp, err := call[Tenant](ctx, c, &request{method: http.MethodGet, path: "/tenants/" + esc(ref)})
	if err != nil {
		return nil, err
	}
	t.ETag = etagFor(resp, t.Version)
	return t, nil
}

// ListTenants lists every tenant.
func (c *Client) ListTenants(ctx context.Context) ([]Tenant, error) {
	return listAll[Tenant](ctx, c, "/tenants", nil, nil)
}

// UpdateTenant patches a tenant.
func (c *Client) UpdateTenant(ctx context.Context, ref, etag string, in TenantUpdate) (*Tenant, error) {
	t, resp, err := call[Tenant](ctx, c, &request{method: http.MethodPatch, path: "/tenants/" + esc(ref), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	t.ETag = etagFor(resp, t.Version)
	return t, nil
}

// DeleteTenant deletes a tenant and everything it owns.
func (c *Client) DeleteTenant(ctx context.Context, ref, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: "/tenants/" + esc(ref), ifMatch: etag})
	return err
}

// User is an installation user.
type User struct {
	ID           string    `json:"id"`
	PublicID     string    `json:"public_id"`
	DisplayName  string    `json:"display_name"`
	PrimaryEmail string    `json:"primary_email,omitempty"`
	Status       string    `json:"status"`
	Version      int64     `json:"version"`
	ManagedBy    string    `json:"managed_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ETag         string    `json:"-"`
}

// UserCreate creates a user.
type UserCreate struct {
	DisplayName  string `json:"display_name"`
	PrimaryEmail string `json:"primary_email,omitempty"`
}

// UserUpdate edits a user; nil fields are unchanged ("" clears the email).
type UserUpdate struct {
	DisplayName  *string `json:"display_name,omitempty"`
	PrimaryEmail *string `json:"primary_email,omitempty"`
	Status       *string `json:"status,omitempty"`
}

// CreateUser creates a user.
func (c *Client) CreateUser(ctx context.Context, in UserCreate) (*User, error) {
	u, cr, err := create[User](ctx, c, "/users", in)
	if err != nil {
		return nil, err
	}
	u.ETag = etagFor(cr.resp, u.Version)
	return u, nil
}

// GetUser reads a user by ID.
func (c *Client) GetUser(ctx context.Context, id string) (*User, error) {
	u, resp, err := call[User](ctx, c, &request{method: http.MethodGet, path: "/users/" + esc(id)})
	if err != nil {
		return nil, err
	}
	u.ETag = etagFor(resp, u.Version)
	return u, nil
}

// FindUserByEmail returns the user whose primary email matches exactly (case-insensitive).
func (c *Client) FindUserByEmail(ctx context.Context, email string) (*User, error) {
	q := url.Values{"email": {email}}
	us, err := listAll[User](ctx, c, "/users", q, nil)
	if err != nil {
		return nil, err
	}
	if len(us) == 0 {
		return nil, notFound("/users?email", "user with that email")
	}
	u := us[0]
	u.ETag = ETagOf(u.Version)
	return &u, nil
}

// UpdateUser patches a user.
func (c *Client) UpdateUser(ctx context.Context, id, etag string, in UserUpdate) (*User, error) {
	u, resp, err := call[User](ctx, c, &request{method: http.MethodPatch, path: "/users/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	u.ETag = etagFor(resp, u.Version)
	return u, nil
}

// DeleteUser deletes a user.
func (c *Client) DeleteUser(ctx context.Context, id, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: "/users/" + esc(id), ifMatch: etag})
	return err
}

// Member is a tenant membership.
type Member struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	UserID    string `json:"user_id"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	Version   int64  `json:"version"`
	ManagedBy string `json:"managed_by"`
	ETag      string `json:"-"`
}

// AddMember adds a user to a tenant.
func (c *Client) AddMember(ctx context.Context, tenant, userID string) (*Member, error) {
	m, cr, err := create[Member](ctx, c, "/tenants/"+esc(tenant)+"/members", map[string]string{"user_id": userID})
	if err != nil {
		return nil, err
	}
	m.ETag = etagFor(cr.resp, m.Version)
	return m, nil
}

// GetMember finds a membership by ID (the API has no single-member read, so the member list is scanned).
func (c *Client) GetMember(ctx context.Context, tenant, id string) (*Member, error) {
	path := "/tenants/" + esc(tenant) + "/members"
	ms, err := listAll[Member](ctx, c, path, nil, func(m Member) bool { return m.ID == id })
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if m.ID == id {
			m.ETag = ETagOf(m.Version)
			return &m, nil
		}
	}
	return nil, notFound(path, "membership "+id)
}

// SetMemberStatus suspends or reactivates a membership.
func (c *Client) SetMemberStatus(ctx context.Context, tenant, id, etag, status string) (*Member, error) {
	m, resp, err := call[Member](ctx, c, &request{method: http.MethodPatch, path: "/tenants/" + esc(tenant) + "/members/" + esc(id),
		body: map[string]string{"status": status}, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	m.ETag = etagFor(resp, m.Version)
	return m, nil
}

// RemoveMember removes a membership.
func (c *Client) RemoveMember(ctx context.Context, tenant, id, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: "/tenants/" + esc(tenant) + "/members/" + esc(id), ifMatch: etag})
	return err
}

// Invite is a pending invitation.
type Invite struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Email      string     `json:"email"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

// InviteCreate invites an email address.
type InviteCreate struct {
	Email        string `json:"email"`
	ValidSeconds int64  `json:"valid_seconds,omitempty"`
}

// CreateInvite creates an invitation.
func (c *Client) CreateInvite(ctx context.Context, tenant string, in InviteCreate) (*Invite, error) {
	i, _, err := create[Invite](ctx, c, "/tenants/"+esc(tenant)+"/invites", in)
	return i, err
}

// GetInvite finds an invitation by ID (list scan; the API has no single-invite read).
func (c *Client) GetInvite(ctx context.Context, tenant, id string) (*Invite, error) {
	path := "/tenants/" + esc(tenant) + "/invites"
	is, _, err := call[[]Invite](ctx, c, &request{method: http.MethodGet, path: path})
	if err != nil {
		return nil, err
	}
	for _, i := range *is {
		if i.ID == id {
			return &i, nil
		}
	}
	return nil, notFound(path, "invite "+id)
}

// DeleteInvite revokes an invitation.
func (c *Client) DeleteInvite(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: "/tenants/" + esc(tenant) + "/invites/" + esc(id)})
	return err
}

// IdentityProvider is an upstream login provider (its client secret is write-only).
type IdentityProvider struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"client_id"`
	AllowedHDs  []string `json:"allowed_hds"`
	Scopes      string   `json:"scopes"`
	Enabled     bool     `json:"enabled"`
	CallbackURL string   `json:"callback_url"`
	Version     int64    `json:"version"`
	ETag        string   `json:"-"`
}

// IdentityProviderCreate adds a provider.
type IdentityProviderCreate struct {
	Slug         string   `json:"slug"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Issuer       string   `json:"issuer,omitempty"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	AllowedHDs   []string `json:"allowed_hds,omitempty"`
	Scopes       string   `json:"scopes,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
}

// IdentityProviderUpdate edits a provider; empty fields are unchanged (AllowedHDs: nil = unchanged, empty = clear).
type IdentityProviderUpdate struct {
	Name         string    `json:"name,omitempty"`
	Issuer       string    `json:"issuer,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	AllowedHDs   *[]string `json:"allowed_hds,omitempty"`
	Scopes       string    `json:"scopes,omitempty"`
	Enabled      *bool     `json:"enabled,omitempty"`
}

// CreateIdentityProvider adds a provider.
func (c *Client) CreateIdentityProvider(ctx context.Context, in IdentityProviderCreate) (*IdentityProvider, error) {
	p, cr, err := create[IdentityProvider](ctx, c, "/identity-providers", in)
	if err != nil {
		return nil, err
	}
	p.ETag = etagFor(cr.resp, p.Version)
	return p, nil
}

// GetIdentityProvider finds a provider by ID or slug (list scan; the API has no single-provider read).
func (c *Client) GetIdentityProvider(ctx context.Context, ref string) (*IdentityProvider, error) {
	ps, _, err := call[[]IdentityProvider](ctx, c, &request{method: http.MethodGet, path: "/identity-providers"})
	if err != nil {
		return nil, err
	}
	for _, p := range *ps {
		if p.ID == ref || p.Slug == ref {
			p.ETag = ETagOf(p.Version)
			return &p, nil
		}
	}
	return nil, notFound("/identity-providers", "identity provider "+ref)
}

// UpdateIdentityProvider patches a provider (by ID).
func (c *Client) UpdateIdentityProvider(ctx context.Context, id, etag string, in IdentityProviderUpdate) (*IdentityProvider, error) {
	p, resp, err := call[IdentityProvider](ctx, c, &request{method: http.MethodPatch, path: "/identity-providers/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	p.ETag = etagFor(resp, p.Version)
	return p, nil
}

// DeleteIdentityProvider deletes a provider (by ID).
func (c *Client) DeleteIdentityProvider(ctx context.Context, id, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: "/identity-providers/" + esc(id), ifMatch: etag})
	return err
}

// DomainRule enrolls a domain into a tenant.
type DomainRule struct {
	ID           string `json:"id"`
	Domain       string `json:"domain"`
	TenantID     string `json:"tenant_id"`
	ProviderID   string `json:"provider_id"`
	EvidenceKind string `json:"evidence_kind"`
	Mode         string `json:"mode"`
	Version      int64  `json:"version"`
	ETag         string `json:"-"`
}

// DomainRuleCreate adds a rule.
type DomainRuleCreate struct {
	Domain       string `json:"domain"`
	ProviderID   string `json:"provider_id"`
	EvidenceKind string `json:"evidence_kind"`
	Mode         string `json:"mode"`
}

// CreateDomainRule adds a domain rule.
func (c *Client) CreateDomainRule(ctx context.Context, tenant string, in DomainRuleCreate) (*DomainRule, error) {
	r, cr, err := create[DomainRule](ctx, c, "/tenants/"+esc(tenant)+"/domain-rules", in)
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(cr.resp, r.Version)
	return r, nil
}

// GetDomainRule finds a rule by ID (list scan).
func (c *Client) GetDomainRule(ctx context.Context, tenant, id string) (*DomainRule, error) {
	path := "/tenants/" + esc(tenant) + "/domain-rules"
	rs, _, err := call[[]DomainRule](ctx, c, &request{method: http.MethodGet, path: path})
	if err != nil {
		return nil, err
	}
	for _, r := range *rs {
		if r.ID == id {
			r.ETag = ETagOf(r.Version)
			return &r, nil
		}
	}
	return nil, notFound(path, "domain rule "+id)
}

// DeleteDomainRule deletes a rule.
func (c *Client) DeleteDomainRule(ctx context.Context, tenant, id, etag string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: "/tenants/" + esc(tenant) + "/domain-rules/" + esc(id), ifMatch: etag})
	return err
}
