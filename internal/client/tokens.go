package client

import (
	"context"
	"net/http"
	"time"
)

// AutomationToken is an automation token as the API describes it (never with its secret, except right after creation).
type AutomationToken struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"` // installation or tenant
	TenantID     string     `json:"tenant_id,omitempty"`
	Role         string     `json:"role,omitempty"`
	Permissions  []string   `json:"permissions"`
	Hint         string     `json:"hint"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	NeverExpires bool       `json:"never_expires"`
	IssuedBy     string     `json:"issued_by,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	// Token is the secret, present only in the creation response.
	Token string `json:"token,omitempty"`
}

// InstallationTokenCreate issues an installation token (role admin, auditor or inspector).
type InstallationTokenCreate struct {
	Name          string `json:"name"`
	Role          string `json:"role"`
	ExpiresInDays int64  `json:"expires_in_days,omitempty"`
	NeverExpires  bool   `json:"never_expires,omitempty"`
}

// TenantTokenCreate issues a tenant token.
type TenantTokenCreate struct {
	Name          string   `json:"name"`
	Permissions   []string `json:"permissions"`
	ExpiresInDays int64    `json:"expires_in_days"`
}

const installationTokensPath = "/installation/automation-tokens"

func tenantTokensPath(tenant string) string { return "/tenants/" + esc(tenant) + "/automation-tokens" }

// tokensPath is the collection of installation tokens (tenant "") or of a tenant's tokens.
func tokensPath(tenant string) string {
	if tenant == "" {
		return installationTokensPath
	}
	return tenantTokensPath(tenant)
}

// CreateInstallationToken issues an installation token; its secret is in Token.
func (c *Client) CreateInstallationToken(ctx context.Context, in InstallationTokenCreate) (*AutomationToken, Created, error) {
	t, cr, err := create[AutomationToken](ctx, c, installationTokensPath, in)
	if err != nil {
		return nil, Created{}, err
	}
	return t, Created{SecretWithheld: cr.withheld}, nil
}

// CreateTenantToken issues a tenant token; its secret is in Token.
func (c *Client) CreateTenantToken(ctx context.Context, tenant string, in TenantTokenCreate) (*AutomationToken, Created, error) {
	t, cr, err := create[AutomationToken](ctx, c, tenantTokensPath(tenant), in)
	if err != nil {
		return nil, Created{}, err
	}
	return t, Created{SecretWithheld: cr.withheld}, nil
}

// GetAutomationToken reads an installation token (tenant "") or a tenant's token.
func (c *Client) GetAutomationToken(ctx context.Context, tenant, id string) (*AutomationToken, error) {
	t, _, err := call[AutomationToken](ctx, c, &request{method: http.MethodGet, path: tokensPath(tenant) + "/" + esc(id)})
	if err != nil {
		return nil, err
	}
	t.Token = ""
	return t, nil
}

// RevokeAutomationToken revokes a token and every token it issued.
func (c *Client) RevokeAutomationToken(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tokensPath(tenant) + "/" + esc(id)})
	return err
}
