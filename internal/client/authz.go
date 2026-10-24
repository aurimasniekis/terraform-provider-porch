package client

import (
	"context"
	"net/http"
	"slices"
	"time"
)

func tpath(tenant, kind string) string { return "/tenants/" + esc(tenant) + "/" + kind }

// Role is a tenant role.
type Role struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	BuiltIn     bool     `json:"built_in"`
	Permissions []string `json:"permissions"`
	Includes    []string `json:"includes"`
	Version     int64    `json:"version"`
	ETag        string   `json:"-"`
}

// RoleRequest creates or replaces a role (slug is create-only).
type RoleRequest struct {
	Slug        string   `json:"slug,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Permissions []string `json:"permissions"`
	Includes    []string `json:"includes,omitempty"`
}

// CreateRole creates a role.
func (c *Client) CreateRole(ctx context.Context, tenant string, in RoleRequest) (*Role, error) {
	if in.Permissions == nil {
		in.Permissions = []string{}
	}
	r, cr, err := create[Role](ctx, c, tpath(tenant, "roles"), in)
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(cr.resp, r.Version)
	return r, nil
}

// GetRole reads a role by ID or slug.
func (c *Client) GetRole(ctx context.Context, tenant, ref string) (*Role, error) {
	r, resp, err := call[Role](ctx, c, &request{method: http.MethodGet, path: tpath(tenant, "roles") + "/" + esc(ref)})
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(resp, r.Version)
	return r, nil
}

// ReplaceRole replaces a role's definition (PUT).
func (c *Client) ReplaceRole(ctx context.Context, tenant, id, etag string, in RoleRequest) (*Role, error) {
	in.Slug = ""
	if in.Permissions == nil {
		in.Permissions = []string{}
	}
	r, resp, err := call[Role](ctx, c, &request{method: http.MethodPut, path: tpath(tenant, "roles") + "/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(resp, r.Version)
	return r, nil
}

// DeleteRole deletes a custom role and its assignments.
func (c *Client) DeleteRole(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "roles") + "/" + esc(id)})
	return err
}

// Group is a tenant group with its direct members and parents.
type Group struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parents     []string `json:"parents"`
	Members     []string `json:"members"`
	Version     int64    `json:"version"`
	ETag        string   `json:"-"`
}

// GroupCreate creates a group.
type GroupCreate struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// GroupUpdate replaces a group's name and description.
type GroupUpdate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateGroup creates a group.
func (c *Client) CreateGroup(ctx context.Context, tenant string, in GroupCreate) (*Group, error) {
	g, cr, err := create[Group](ctx, c, tpath(tenant, "groups"), in)
	if err != nil {
		return nil, err
	}
	g.ETag = etagFor(cr.resp, g.Version)
	return g, nil
}

// GetGroup reads a group by ID or slug.
func (c *Client) GetGroup(ctx context.Context, tenant, ref string) (*Group, error) {
	g, resp, err := call[Group](ctx, c, &request{method: http.MethodGet, path: tpath(tenant, "groups") + "/" + esc(ref)})
	if err != nil {
		return nil, err
	}
	g.ETag = etagFor(resp, g.Version)
	return g, nil
}

// UpdateGroup renames or describes a group.
func (c *Client) UpdateGroup(ctx context.Context, tenant, id, etag string, in GroupUpdate) (*Group, error) {
	g, resp, err := call[Group](ctx, c, &request{method: http.MethodPatch, path: tpath(tenant, "groups") + "/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	g.ETag = etagFor(resp, g.Version)
	return g, nil
}

// DeleteGroup deletes a group.
func (c *Client) DeleteGroup(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "groups") + "/" + esc(id)})
	return err
}

// AddGroupMember adds a tenant member to a group. Porch answers 422 when the user is already in the group, so a
// failed call is followed by a read to make the operation idempotent.
func (c *Client) AddGroupMember(ctx context.Context, tenant, group, user string) error {
	_, err := c.do(ctx, &request{method: http.MethodPut, path: tpath(tenant, "groups") + "/" + esc(group) + "/members/" + esc(user)})
	if err != nil && HasCode(err, "validation_failed") {
		if g, gerr := c.GetGroup(ctx, tenant, group); gerr == nil && slices.Contains(g.Members, user) {
			return nil
		}
	}
	return err
}

// RemoveGroupMember removes a user from a group.
func (c *Client) RemoveGroupMember(ctx context.Context, tenant, group, user string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "groups") + "/" + esc(group) + "/members/" + esc(user)})
	return err
}

// NestGroup makes group a member of parent.
func (c *Client) NestGroup(ctx context.Context, tenant, group, parent string) error {
	_, err := c.do(ctx, &request{method: http.MethodPut, path: tpath(tenant, "groups") + "/" + esc(group) + "/parents/" + esc(parent)})
	if err != nil && HasCode(err, "validation_failed") {
		g, gerr := c.GetGroup(ctx, tenant, group)
		p, perr := c.GetGroup(ctx, tenant, parent)
		if gerr == nil && perr == nil && slices.Contains(g.Parents, p.ID) {
			return nil
		}
	}
	return err
}

// UnnestGroup removes a nesting edge.
func (c *Client) UnnestGroup(ctx context.Context, tenant, group, parent string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "groups") + "/" + esc(group) + "/parents/" + esc(parent)})
	return err
}

// Resource is an authorization resource (assignment scope).
type Resource struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Slug       string            `json:"slug"`
	Name       string            `json:"name"`
	ParentID   string            `json:"parent_id,omitempty"`
	Attributes map[string]string `json:"attributes"`
	Version    int64             `json:"version"`
	ETag       string            `json:"-"`
}

// ResourceCreate creates a resource.
type ResourceCreate struct {
	Type       string            `json:"type"`
	Slug       string            `json:"slug"`
	Name       string            `json:"name"`
	ParentID   string            `json:"parent_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ResourceUpdate replaces a resource's name and attributes.
type ResourceUpdate struct {
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes"`
}

// CreateResource creates a resource.
func (c *Client) CreateResource(ctx context.Context, tenant string, in ResourceCreate) (*Resource, error) {
	r, cr, err := create[Resource](ctx, c, tpath(tenant, "resources"), in)
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(cr.resp, r.Version)
	return r, nil
}

// GetResource reads a resource by ID.
func (c *Client) GetResource(ctx context.Context, tenant, id string) (*Resource, error) {
	r, resp, err := call[Resource](ctx, c, &request{method: http.MethodGet, path: tpath(tenant, "resources") + "/" + esc(id)})
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(resp, r.Version)
	return r, nil
}

// UpdateResource patches a resource.
func (c *Client) UpdateResource(ctx context.Context, tenant, id, etag string, in ResourceUpdate) (*Resource, error) {
	if in.Attributes == nil {
		in.Attributes = map[string]string{}
	}
	r, resp, err := call[Resource](ctx, c, &request{method: http.MethodPatch, path: tpath(tenant, "resources") + "/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	r.ETag = etagFor(resp, r.Version)
	return r, nil
}

// DeleteResource deletes a resource, its children and assignments scoped to it.
func (c *Client) DeleteResource(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "resources") + "/" + esc(id)})
	return err
}

// Policy is a condition or deny policy.
type Policy struct {
	ID         string   `json:"id"`
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	Effect     string   `json:"effect"`
	Actions    []string `json:"actions"`
	Expression string   `json:"expression"`
	Version    int64    `json:"version"`
	ETag       string   `json:"-"`
}

// PolicyRequest creates or replaces a policy (slug is create-only).
type PolicyRequest struct {
	Slug       string   `json:"slug,omitempty"`
	Name       string   `json:"name"`
	Effect     string   `json:"effect"`
	Actions    []string `json:"actions,omitempty"`
	Expression string   `json:"expression"`
}

// CreatePolicy creates a policy.
func (c *Client) CreatePolicy(ctx context.Context, tenant string, in PolicyRequest) (*Policy, error) {
	p, cr, err := create[Policy](ctx, c, tpath(tenant, "policies"), in)
	if err != nil {
		return nil, err
	}
	p.ETag = etagFor(cr.resp, p.Version)
	return p, nil
}

// GetPolicy reads a policy by ID or slug.
func (c *Client) GetPolicy(ctx context.Context, tenant, ref string) (*Policy, error) {
	p, resp, err := call[Policy](ctx, c, &request{method: http.MethodGet, path: tpath(tenant, "policies") + "/" + esc(ref)})
	if err != nil {
		return nil, err
	}
	p.ETag = etagFor(resp, p.Version)
	return p, nil
}

// ReplacePolicy replaces a policy (PUT).
func (c *Client) ReplacePolicy(ctx context.Context, tenant, id, etag string, in PolicyRequest) (*Policy, error) {
	in.Slug = ""
	p, resp, err := call[Policy](ctx, c, &request{method: http.MethodPut, path: tpath(tenant, "policies") + "/" + esc(id), body: in, ifMatch: etag})
	if err != nil {
		return nil, err
	}
	p.ETag = etagFor(resp, p.Version)
	return p, nil
}

// DeletePolicy deletes a policy and the conditional assignments using it.
func (c *Client) DeletePolicy(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "policies") + "/" + esc(id)})
	return err
}

// Assignment binds a subject to a role at a scope.
type Assignment struct {
	ID                string     `json:"id"`
	SubjectType       string     `json:"subject_type"`
	SubjectID         string     `json:"subject_id"`
	RoleID            string     `json:"role_id"`
	ScopeType         string     `json:"scope_type"`
	ScopeResourceType string     `json:"scope_resource_type,omitempty"`
	ScopeResourceID   string     `json:"scope_resource_id,omitempty"`
	ConditionPolicyID string     `json:"condition_policy_id,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	Source            string     `json:"source"`
}

// AssignmentCreate assigns a role.
type AssignmentCreate struct {
	SubjectType       string     `json:"subject_type"`
	SubjectID         string     `json:"subject_id"`
	RoleID            string     `json:"role_id"`
	ScopeType         string     `json:"scope_type"`
	ScopeResourceType string     `json:"scope_resource_type,omitempty"`
	ScopeResourceID   string     `json:"scope_resource_id,omitempty"`
	ConditionPolicyID string     `json:"condition_policy_id,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

// CreateAssignment assigns a role.
func (c *Client) CreateAssignment(ctx context.Context, tenant string, in AssignmentCreate) (*Assignment, error) {
	a, _, err := create[Assignment](ctx, c, tpath(tenant, "assignments"), in)
	return a, err
}

// GetAssignment finds an assignment by ID (list scan; the API has no single-assignment read).
func (c *Client) GetAssignment(ctx context.Context, tenant, id string) (*Assignment, error) {
	path := tpath(tenant, "assignments")
	as, _, err := call[[]Assignment](ctx, c, &request{method: http.MethodGet, path: path})
	if err != nil {
		return nil, err
	}
	for _, a := range *as {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, notFound(path, "assignment "+id)
}

// DeleteAssignment removes an assignment.
func (c *Client) DeleteAssignment(ctx context.Context, tenant, id string) error {
	_, err := c.do(ctx, &request{method: http.MethodDelete, path: tpath(tenant, "assignments") + "/" + esc(id)})
	return err
}
