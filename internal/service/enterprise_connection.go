package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	EnterpriseConnectionUsableByBindings = "AUTHORIZED_BINDINGS"
)

type CreateEnterpriseConnectionRequest struct {
	ProviderName            string `json:"provider_name"`
	DisplayName             string `json:"display_name"`
	BaseURL                 string `json:"base_url"`
	CredentialRef           string `json:"credential_ref"`
	CredentialVersion       string `json:"credential_version"`
	CredentialPolicyVersion string `json:"credential_policy_version"`
	Visibility              string `json:"visibility"`
	UsableBy                string `json:"usable_by"`
	ManagedBy               string `json:"managed_by"`
	CredentialScope         string `json:"credential_scope"`
}

type UpdateEnterpriseConnectionRequest struct {
	ProviderName            string `json:"provider_name"`
	DisplayName             string `json:"display_name"`
	BaseURL                 string `json:"base_url"`
	CredentialRef           string `json:"credential_ref"`
	CredentialVersion       string `json:"credential_version"`
	CredentialPolicyVersion string `json:"credential_policy_version"`
	Visibility              string `json:"visibility"`
	UsableBy                string `json:"usable_by"`
	ManagedBy               string `json:"managed_by"`
	CredentialScope         string `json:"credential_scope"`
}

type CreateEnterpriseBindingRequest struct {
	ConnectionID        string   `json:"connection_id"`
	TenantID            string   `json:"tenant_id"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
	AllowedModelRefs    []string `json:"allowed_model_refs"`
	ApprovalID          string   `json:"approval_id"`
}

type UpdateEnterpriseBindingRequest struct {
	AllowedCapabilities []string `json:"allowed_capabilities"`
	AllowedModelRefs    []string `json:"allowed_model_refs"`
	ApprovalID          string   `json:"approval_id"`
}

type PinEnterpriseModelRouteRequest struct {
	ConnectionID      string `json:"connection_id"`
	ConnectionVersion int64  `json:"connection_version"`
	BindingID         string `json:"binding_id"`
	BindingVersion    int64  `json:"binding_version"`
	ModelRef          string `json:"model_ref"`
}

type EnterpriseConnectionView struct {
	Connection model.EnterpriseConnection        `json:"connection"`
	Version    model.EnterpriseConnectionVersion `json:"version"`
	TenantName string                            `json:"tenant_name,omitempty"`
}

type EnterpriseConnectionList struct {
	Items      []EnterpriseConnectionView `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
	HasMore    bool                       `json:"has_more"`
}

type enterpriseConnectionCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

type EnterpriseBindingView struct {
	Binding    model.EnterpriseConnectionBinding        `json:"binding"`
	Version    model.EnterpriseConnectionBindingVersion `json:"version"`
	TenantName string                                   `json:"tenant_name,omitempty"`
}

func normalizeEnterpriseConnection(req *CreateEnterpriseConnectionRequest) error {
	req.ProviderName = strings.TrimSpace(req.ProviderName)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.CredentialRef = strings.TrimSpace(req.CredentialRef)
	req.CredentialVersion = strings.TrimSpace(req.CredentialVersion)
	req.CredentialPolicyVersion = strings.TrimSpace(req.CredentialPolicyVersion)
	if req.ProviderName == "" || req.DisplayName == "" {
		return httperr.BadRequest(40060, "provider_name and display_name are required")
	}
	if req.CredentialRef == "" {
		return httperr.BadRequest(40060, "credential_ref is required")
	}
	if req.CredentialVersion == "" {
		req.CredentialVersion = "v1"
	}
	if req.CredentialPolicyVersion == "" {
		req.CredentialPolicyVersion = "v1"
	}
	if req.Visibility == "" {
		req.Visibility = model.EnterpriseConnectionVisibilityPrivate
	}
	if req.UsableBy == "" {
		req.UsableBy = EnterpriseConnectionUsableByBindings
	}
	if req.ManagedBy == "" {
		req.ManagedBy = model.EnterpriseConnectionManagedByPlatform
	}
	if req.CredentialScope == "" {
		req.CredentialScope = model.EnterpriseConnectionCredentialScopeConnection
	}
	switch req.Visibility {
	case model.EnterpriseConnectionVisibilityPrivate, model.EnterpriseConnectionVisibilityShared:
	default:
		return httperr.BadRequest(40061, "invalid connection visibility")
	}
	if req.UsableBy != EnterpriseConnectionUsableByBindings {
		return httperr.BadRequest(40061, "connection usable_by must be AUTHORIZED_BINDINGS")
	}
	if req.ManagedBy != model.EnterpriseConnectionManagedByPlatform && req.ManagedBy != model.EnterpriseConnectionManagedByWorkspace {
		return httperr.BadRequest(40061, "invalid managed_by")
	}
	switch req.CredentialScope {
	case model.EnterpriseConnectionCredentialScopeConnection, model.EnterpriseConnectionCredentialScopeBinding:
	default:
		return httperr.BadRequest(40061, "invalid credential_scope")
	}
	return nil
}

func updateEnterpriseConnectionFields(existing *model.EnterpriseConnectionVersion, req *UpdateEnterpriseConnectionRequest) error {
	createReq := CreateEnterpriseConnectionRequest{
		ProviderName: req.ProviderName, DisplayName: req.DisplayName, BaseURL: req.BaseURL,
		CredentialRef: req.CredentialRef, CredentialVersion: req.CredentialVersion,
		CredentialPolicyVersion: req.CredentialPolicyVersion, Visibility: req.Visibility,
		UsableBy: req.UsableBy, ManagedBy: req.ManagedBy, CredentialScope: req.CredentialScope,
	}
	if err := normalizeEnterpriseConnection(&createReq); err != nil {
		return err
	}
	existing.ProviderName = createReq.ProviderName
	existing.DisplayName = createReq.DisplayName
	existing.BaseURL = createReq.BaseURL
	existing.CredentialRef = createReq.CredentialRef
	existing.CredentialVersion = createReq.CredentialVersion
	existing.CredentialPolicyVersion = createReq.CredentialPolicyVersion
	existing.Visibility = req.Visibility
	existing.UsableBy = req.UsableBy
	existing.ManagedBy = req.ManagedBy
	existing.CredentialScope = req.CredentialScope
	return nil
}

func enterpriseConnectionConfigHash(version *model.EnterpriseConnectionVersion) string {
	canonical, _ := json.Marshal(struct {
		ProviderName            string `json:"provider_name"`
		DisplayName             string `json:"display_name"`
		BaseURL                 string `json:"base_url"`
		CredentialRef           string `json:"credential_ref"`
		CredentialVersion       string `json:"credential_version"`
		CredentialPolicyVersion string `json:"credential_policy_version"`
		Visibility              string `json:"visibility"`
		UsableBy                string `json:"usable_by"`
		ManagedBy               string `json:"managed_by"`
		CredentialScope         string `json:"credential_scope"`
	}{
		version.ProviderName, version.DisplayName, version.BaseURL, version.CredentialRef,
		version.CredentialVersion, version.CredentialPolicyVersion,
		version.Visibility, version.UsableBy, version.ManagedBy, version.CredentialScope,
	})
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeStringList(values []string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, httperr.BadRequest(40062, "authorization list values must be non-empty")
		}
		if seen[value] {
			return nil, httperr.BadRequest(40062, "authorization list values must be unique")
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func encodeStringList(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func decodeStringList(raw string) []string {
	var values []string
	if raw == "" || json.Unmarshal([]byte(raw), &values) != nil {
		return []string{}
	}
	return values
}

// CreateEnterpriseConnection creates a platform-owned connection and its first
// immutable configuration version.
func (s *Service) CreateEnterpriseConnection(ctx context.Context, actorID, ownerTenantID string, req CreateEnterpriseConnectionRequest) (*EnterpriseConnectionView, error) {
	if strings.TrimSpace(ownerTenantID) == "" {
		return nil, httperr.BadRequest(40060, "owner tenant is required")
	}
	ownerTenant, err := s.Store.GetTenant(ctx, ownerTenantID)
	if err != nil {
		return nil, err
	}
	if ownerTenant == nil {
		return nil, httperr.BadRequest(40060, "owner tenant not found")
	}
	if err := normalizeEnterpriseConnection(&req); err != nil {
		return nil, err
	}
	provisional := model.EnterpriseConnection{OwnerTenantID: ownerTenantID, ManagedBy: req.ManagedBy, Visibility: req.Visibility}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, &provisional, "tenant.connection.manage"); err != nil {
		return nil, err
	}
	if ownerTenant.Type != model.TenantTypePlatform && (req.Visibility == model.EnterpriseConnectionVisibilityShared ||
		req.ManagedBy == model.EnterpriseConnectionManagedByPlatform) {
		return nil, httperr.Forbidden("shared or platform-managed connections must be platform-owned")
	}
	if err := ValidateProviderBaseURL(req.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	connection := &model.EnterpriseConnection{
		ID: id.New(), OwnerTenantID: ownerTenantID, ManagedBy: req.ManagedBy,
		Visibility: req.Visibility, UsableBy: req.UsableBy, CredentialScope: req.CredentialScope,
		CurrentConnectionVersion: 1, LifecycleStatus: model.EnterpriseLifecycleActive,
		RuntimeHealth: model.RuntimeHealthUnknown, CreatedAt: now, UpdatedAt: now,
	}
	version := &model.EnterpriseConnectionVersion{
		ConnectionID: connection.ID, Version: 1, ProviderName: req.ProviderName,
		DisplayName: req.DisplayName, BaseURL: req.BaseURL, CredentialRef: req.CredentialRef,
		CredentialVersion: req.CredentialVersion, CredentialPolicyVersion: req.CredentialPolicyVersion,
		Visibility: req.Visibility, UsableBy: req.UsableBy,
		ManagedBy: req.ManagedBy, CredentialScope: req.CredentialScope,
		CreatedAt: now,
	}
	version.ConnectionConfigHash = enterpriseConnectionConfigHash(version)
	if err := s.Store.CreateEnterpriseConnection(ctx, connection, version); err != nil {
		return nil, err
	}
	return &EnterpriseConnectionView{Connection: *connection, Version: *version}, nil
}

// UpdateEnterpriseConnection creates a new immutable connection version. The
// current pointer moves only after the version insert succeeds.
func (s *Service) UpdateEnterpriseConnection(ctx context.Context, actorID, connectionID string, req UpdateEnterpriseConnectionRequest) (*EnterpriseConnectionView, error) {
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	if connection.LifecycleStatus == model.EnterpriseLifecycleDisabled || connection.LifecycleStatus == model.EnterpriseLifecycleRetired {
		return nil, httperr.BadRequest(40063, "disabled or retired connection cannot be edited")
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.manage"); err != nil {
		return nil, err
	}
	version, err := s.Store.GetEnterpriseConnectionVersion(ctx, connectionID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
	}
	updated := *version
	if err := updateEnterpriseConnectionFields(&updated, &req); err != nil {
		return nil, err
	}
	if err := ValidateProviderBaseURL(updated.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	updated.ConnectionConfigHash = enterpriseConnectionConfigHash(&updated)
	if updated.ConnectionConfigHash == version.ConnectionConfigHash {
		return &EnterpriseConnectionView{Connection: *connection, Version: *version}, nil
	}
	existingVersions, err := s.Store.ListEnterpriseConnectionVersions(ctx, []string{connectionID})
	if err != nil {
		return nil, err
	}
	nextVersion := connection.CurrentConnectionVersion + 1
	for _, existing := range existingVersions {
		if existing.Version >= nextVersion {
			nextVersion = existing.Version + 1
		}
	}
	updated.Version = nextVersion
	next := updated
	if err := s.Store.CreateEnterpriseConnectionVersion(ctx, connection, &next); err != nil {
		return nil, err
	}
	connection.CurrentConnectionVersion = next.Version
	return &EnterpriseConnectionView{Connection: *connection, Version: next}, nil
}

func (s *Service) ListEnterpriseConnections(ctx context.Context, tenantIDs []string, providerName, lifecycleStatus string, page, pageSize int) ([]EnterpriseConnectionView, int64, error) {
	connections, total, err := s.Store.ListEnterpriseConnections(ctx, tenantIDs, providerName, lifecycleStatus, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(connections))
	for _, connection := range connections {
		ids = append(ids, connection.ID)
	}
	versions, err := s.Store.ListEnterpriseConnectionVersions(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	versionByID := make(map[string]model.EnterpriseConnectionVersion, len(connections))
	for _, version := range versions {
		versionByID[version.ConnectionID] = version
	}
	tenantNames := s.tenantNameMap(ctx)
	views := make([]EnterpriseConnectionView, 0, len(connections))
	for _, connection := range connections {
		version, ok := versionByID[connection.ID]
		if !ok {
			continue
		}
		views = append(views, EnterpriseConnectionView{
			Connection: connection, Version: version, TenantName: tenantNames[connection.OwnerTenantID],
		})
	}
	return views, total, nil
}

func (s *Service) ListEnterpriseConnectionsByCursor(ctx context.Context, tenantIDs []string, providerName, lifecycleStatus, cursor string, limit int) (*EnterpriseConnectionList, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var key *repository.EnterpriseConnectionCursor
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, httperr.BadRequest(40060, "invalid enterprise connection cursor")
		}
		var payload enterpriseConnectionCursor
		if err := json.Unmarshal(decoded, &payload); err != nil || payload.ID == "" || payload.CreatedAt.IsZero() {
			return nil, httperr.BadRequest(40060, "invalid enterprise connection cursor")
		}
		key = &repository.EnterpriseConnectionCursor{CreatedAt: payload.CreatedAt.UTC(), ID: payload.ID}
	}
	connections, hasMore, err := s.Store.ListEnterpriseConnectionsByCursor(ctx, tenantIDs, providerName, lifecycleStatus, key, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(connections))
	for _, connection := range connections {
		ids = append(ids, connection.ID)
	}
	versions, err := s.Store.ListEnterpriseConnectionVersions(ctx, ids)
	if err != nil {
		return nil, err
	}
	versionByID := make(map[string]model.EnterpriseConnectionVersion, len(connections))
	for _, version := range versions {
		versionByID[version.ConnectionID] = version
	}
	tenantNames := s.tenantNameMap(ctx)
	views := make([]EnterpriseConnectionView, 0, len(connections))
	for _, connection := range connections {
		version, ok := versionByID[connection.ID]
		if !ok {
			continue
		}
		views = append(views, EnterpriseConnectionView{
			Connection: connection, Version: version, TenantName: tenantNames[connection.OwnerTenantID],
		})
	}
	result := &EnterpriseConnectionList{Items: views, HasMore: hasMore}
	if hasMore && len(connections) > 0 {
		last := connections[len(connections)-1]
		payload, err := json.Marshal(enterpriseConnectionCursor{CreatedAt: last.CreatedAt.UTC(), ID: last.ID})
		if err != nil {
			return nil, err
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(payload)
	}
	return result, nil
}

func (s *Service) GetEnterpriseConnection(ctx context.Context, connectionID string) (*EnterpriseConnectionView, error) {
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	version, err := s.Store.GetEnterpriseConnectionVersion(ctx, connectionID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
	}
	tenantNames := s.tenantNameMap(ctx)
	return &EnterpriseConnectionView{Connection: *connection, Version: *version, TenantName: tenantNames[connection.OwnerTenantID]}, nil
}

func (s *Service) GetEnterpriseConnectionForScope(ctx context.Context, scope TenantScope, connectionID string) (*EnterpriseConnectionView, error) {
	view, err := s.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if !scope.ScopeAll() {
		allowed := false
		for _, tenantID := range scope.AllowedTenantIDs {
			if view.Connection.OwnerTenantID == tenantID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, httperr.NotFound("enterprise connection not found")
		}
	}
	return view, nil
}

func (s *Service) CreateEnterpriseBinding(ctx context.Context, actorID string, req CreateEnterpriseBindingRequest) (*EnterpriseBindingView, error) {
	req.ConnectionID = strings.TrimSpace(req.ConnectionID)
	req.TenantID = strings.TrimSpace(req.TenantID)
	if req.ConnectionID == "" || req.TenantID == "" {
		return nil, httperr.BadRequest(40064, "connection_id and tenant_id are required")
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil || connection.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.BadRequest(40064, "active enterprise connection is required")
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.bind"); err != nil {
		return nil, err
	}
	tenant, err := s.Store.GetTenant(ctx, req.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Type != model.TenantTypeWorkspace {
		return nil, httperr.BadRequest(40064, "binding target must be a workspace tenant")
	}
	if len(req.AllowedModelRefs) == 0 {
		return nil, httperr.BadRequest(40064, "allowed_model_refs is required")
	}
	capabilities, err := normalizeStringList(req.AllowedCapabilities)
	if err != nil {
		return nil, err
	}
	modelRefs, err := normalizeStringList(req.AllowedModelRefs)
	if err != nil {
		return nil, err
	}
	binding := &model.EnterpriseConnectionBinding{
		BindingID: id.New(), ConnectionID: req.ConnectionID, TenantID: req.TenantID,
		CurrentBindingVersion: 1,
		LifecycleStatus:       model.EnterpriseLifecycleActive,
	}
	version := &model.EnterpriseConnectionBindingVersion{
		BindingID: binding.BindingID, Version: 1, TenantID: req.TenantID,
		ConnectionID:        req.ConnectionID,
		AllowedCapabilities: encodeStringList(capabilities), AllowedModelRefs: encodeStringList(modelRefs),
		ApprovalID: strings.TrimSpace(req.ApprovalID), CreatedBy: actorID,
	}
	if err := s.Store.CreateEnterpriseBinding(ctx, binding, version); err != nil {
		return nil, err
	}
	return s.GetEnterpriseBinding(ctx, binding.BindingID)
}

func (s *Service) UpdateEnterpriseBinding(ctx context.Context, actorID, bindingID string, req UpdateEnterpriseBindingRequest) (*EnterpriseBindingView, error) {
	binding, err := s.Store.GetEnterpriseBinding(ctx, bindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("enterprise binding not found")
	}
	if binding.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.BadRequest(40065, "inactive binding cannot be edited")
	}
	current, err := s.Store.GetEnterpriseBindingVersion(ctx, bindingID, binding.CurrentBindingVersion)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, httperr.NotFound("enterprise binding version not found")
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, binding.ConnectionID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.bind"); err != nil {
		return nil, err
	}
	capabilities, err := normalizeStringList(req.AllowedCapabilities)
	if err != nil {
		return nil, err
	}
	modelRefs, err := normalizeStringList(req.AllowedModelRefs)
	if err != nil {
		return nil, err
	}
	if len(modelRefs) == 0 {
		return nil, httperr.BadRequest(40064, "allowed_model_refs is required")
	}
	next := *current
	next.Version = binding.CurrentBindingVersion + 1
	next.AllowedCapabilities = encodeStringList(capabilities)
	next.AllowedModelRefs = encodeStringList(modelRefs)
	next.ApprovalID = strings.TrimSpace(req.ApprovalID)
	next.CreatedBy = actorID
	if err := s.Store.CreateEnterpriseBindingVersion(ctx, binding, &next); err != nil {
		return nil, err
	}
	return s.GetEnterpriseBinding(ctx, bindingID)
}

func (s *Service) RevokeEnterpriseBinding(ctx context.Context, actorID, bindingID string) (*EnterpriseBindingView, error) {
	binding, err := s.Store.GetEnterpriseBinding(ctx, bindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("enterprise binding not found")
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, binding.ConnectionID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.unbind"); err != nil {
		return nil, err
	}
	if err := s.Store.SetEnterpriseBindingLifecycle(ctx, bindingID, model.EnterpriseLifecycleDisabled); err != nil {
		return nil, err
	}
	return s.GetEnterpriseBinding(ctx, bindingID)
}

func (s *Service) ListEnterpriseBindings(ctx context.Context, tenantIDs []string, connectionID, lifecycleStatus string, page, pageSize int) ([]EnterpriseBindingView, int64, error) {
	bindings, total, err := s.Store.ListEnterpriseBindings(ctx, tenantIDs, connectionID, lifecycleStatus, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding.BindingID)
	}
	versions, err := s.Store.ListEnterpriseBindingVersions(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	versionByID := make(map[string]model.EnterpriseConnectionBindingVersion, len(bindings))
	for _, version := range versions {
		versionByID[version.BindingID] = version
	}
	tenantNames := s.tenantNameMap(ctx)
	views := make([]EnterpriseBindingView, 0, len(bindings))
	for _, binding := range bindings {
		version, ok := versionByID[binding.BindingID]
		if !ok {
			continue
		}
		views = append(views, EnterpriseBindingView{
			Binding: binding, Version: version, TenantName: tenantNames[binding.TenantID],
		})
	}
	return views, total, nil
}

func (s *Service) GetEnterpriseBindingForScope(ctx context.Context, scope TenantScope, bindingID string) (*EnterpriseBindingView, error) {
	view, err := s.GetEnterpriseBinding(ctx, bindingID)
	if err != nil {
		return nil, err
	}
	if !scope.ScopeAll() {
		allowed := false
		for _, tenantID := range scope.AllowedTenantIDs {
			if view.Binding.TenantID == tenantID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, httperr.NotFound("enterprise binding not found")
		}
	}
	return view, nil
}

func (s *Service) GetEnterpriseBinding(ctx context.Context, bindingID string) (*EnterpriseBindingView, error) {
	binding, err := s.Store.GetEnterpriseBinding(ctx, bindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("enterprise binding not found")
	}
	version, err := s.Store.GetEnterpriseBindingVersion(ctx, bindingID, binding.CurrentBindingVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise binding version not found")
	}
	tenantNames := s.tenantNameMap(ctx)
	return &EnterpriseBindingView{Binding: *binding, Version: *version, TenantName: tenantNames[binding.TenantID]}, nil
}

func (s *Service) ListEnterpriseConnectionVersions(ctx context.Context, connectionID string) ([]model.EnterpriseConnectionVersion, error) {
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	versions, err := s.Store.ListEnterpriseConnectionVersions(ctx, []string{connectionID})
	if err != nil {
		return nil, err
	}
	return versions, nil
}

func (s *Service) ListEnterpriseBindingVersions(ctx context.Context, bindingID string) ([]EnterpriseBindingView, error) {
	binding, err := s.Store.GetEnterpriseBinding(ctx, bindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("enterprise binding not found")
	}
	rows, err := s.Store.ListEnterpriseBindingVersions(ctx, []string{bindingID})
	if err != nil {
		return nil, err
	}
	tenantNames := s.tenantNameMap(ctx)
	views := make([]EnterpriseBindingView, 0, len(rows))
	for _, version := range rows {
		views = append(views, EnterpriseBindingView{Binding: *binding, Version: version, TenantName: tenantNames[binding.TenantID]})
	}
	return views, nil
}

func (s *Service) tenantNameMap(ctx context.Context) map[string]string {
	names := map[string]string{}
	tenants, err := s.Store.ListAllTenants(ctx)
	if err != nil {
		return names
	}
	for _, tenant := range tenants {
		names[tenant.ID] = tenant.Name
	}
	return names
}

// PinEnterpriseModelRoute pins a workspace route to one exact enterprise
// connection version, binding version and model reference. The pin insert and
// route pointer update are committed atomically.
func (s *Service) PinEnterpriseModelRoute(ctx context.Context, actorID, tenantID, routeID string, req PinEnterpriseModelRouteRequest) (*model.ModelRouteEnterprisePin, error) {
	req.ConnectionID = strings.TrimSpace(req.ConnectionID)
	req.BindingID = strings.TrimSpace(req.BindingID)
	req.ModelRef = strings.TrimSpace(req.ModelRef)
	if routeID == "" || req.ConnectionID == "" || req.BindingID == "" || req.ModelRef == "" {
		return nil, httperr.BadRequest(40065, "route, connection, binding and model_ref are required")
	}
	if req.ConnectionVersion <= 0 || req.BindingVersion <= 0 {
		return nil, httperr.BadRequest(40065, "connection_version and binding_version must be exact")
	}
	route, err := s.Store.GetModelRoute(ctx, tenantID, routeID)
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, httperr.NotFound("model route not found")
	}
	if route.TargetModel != req.ModelRef {
		return nil, httperr.BadRequest(40065, "model_ref must match model route target_model")
	}
	connection, err := s.Store.GetEnterpriseConnectionVersion(ctx, req.ConnectionID, req.ConnectionVersion)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.BadRequest(40065, "enterprise connection version not found")
	}
	connectionIdentity, err := s.Store.GetEnterpriseConnection(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	if connectionIdentity == nil || connectionIdentity.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.BadRequest(40065, "enterprise connection must be active")
	}
	bindingIdentity, err := s.Store.GetEnterpriseBinding(ctx, req.BindingID)
	if err != nil {
		return nil, err
	}
	if bindingIdentity == nil || bindingIdentity.TenantID != tenantID ||
		bindingIdentity.ConnectionID != req.ConnectionID ||
		bindingIdentity.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.BadRequest(40065, "active binding for the route workspace and connection is required")
	}
	binding, err := s.Store.GetEnterpriseBindingVersion(ctx, req.BindingID, req.BindingVersion)
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.ConnectionID != req.ConnectionID || binding.TenantID != tenantID {
		return nil, httperr.BadRequest(40065, "enterprise binding version not found")
	}
	allowed := false
	for _, modelRef := range decodeStringList(binding.AllowedModelRefs) {
		if modelRef == req.ModelRef {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, httperr.Forbidden("model is not authorized by binding version")
	}
	pin := &model.ModelRouteEnterprisePin{
		PinID: id.New(), TenantID: tenantID, RouteID: routeID,
		Version:      route.CurrentPinVersion + 1,
		ConnectionID: req.ConnectionID, ConnectionVersion: req.ConnectionVersion,
		BindingID: req.BindingID, BindingVersion: req.BindingVersion,
		ModelRef: req.ModelRef, CreatedBy: actorID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateModelRouteEnterprisePin(ctx, pin, route); err != nil {
		return nil, err
	}
	return pin, nil
}

func (s *Service) GetEnterpriseModelRoutePin(ctx context.Context, tenantID, routeID, pinID string, version int64) (*model.ModelRouteEnterprisePin, error) {
	pin, err := s.Store.GetModelRouteEnterprisePin(ctx, tenantID, routeID, pinID, version)
	if err != nil {
		return nil, err
	}
	if pin == nil {
		return nil, httperr.NotFound("model route pin not found")
	}
	return pin, nil
}

func (s *Service) ListEnterpriseModelRoutePins(ctx context.Context, tenantID, routeID string) ([]model.ModelRouteEnterprisePin, error) {
	route, err := s.Store.GetModelRoute(ctx, tenantID, routeID)
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, httperr.NotFound("model route not found")
	}
	return s.Store.ListModelRouteEnterprisePins(ctx, tenantID, routeID)
}

// ValidateEnterpriseModelRoutePin is the runtime gate. It never follows a
// current binding/connection pointer and rejects disabled or unauthorized pins.
func (s *Service) ValidateEnterpriseModelRoutePin(ctx context.Context, route *model.ModelRoute) (*model.ModelRouteEnterprisePin, error) {
	if route == nil || route.CurrentPinID == "" || route.CurrentPinVersion <= 0 {
		return nil, nil
	}
	pin, err := s.Store.GetModelRouteEnterprisePin(ctx, route.TenantID, route.ID, route.CurrentPinID, route.CurrentPinVersion)
	if err != nil {
		return nil, err
	}
	if pin == nil {
		return nil, httperr.New(503, 50396, "enterprise model route pin is unavailable")
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, pin.ConnectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil || connection.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.New(503, 50396, "enterprise connection version is unavailable")
	}
	connectionVersion, err := s.Store.GetEnterpriseConnectionVersion(ctx, pin.ConnectionID, pin.ConnectionVersion)
	if err != nil {
		return nil, err
	}
	if connectionVersion == nil {
		return nil, httperr.New(503, 50396, "enterprise connection version is unavailable")
	}
	binding, err := s.Store.GetEnterpriseBinding(ctx, pin.BindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.TenantID != route.TenantID ||
		binding.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.New(503, 50396, "enterprise binding version is unavailable")
	}
	bindingVersion, err := s.Store.GetEnterpriseBindingVersion(ctx, pin.BindingID, pin.BindingVersion)
	if err != nil {
		return nil, err
	}
	if bindingVersion == nil || bindingVersion.ConnectionID != pin.ConnectionID {
		return nil, httperr.New(503, 50396, "enterprise binding version is unavailable")
	}
	allowed := false
	for _, modelRef := range decodeStringList(bindingVersion.AllowedModelRefs) {
		if modelRef == pin.ModelRef {
			allowed = true
			break
		}
	}
	if !allowed || route.TargetModel != pin.ModelRef {
		return nil, httperr.New(503, 50396, "enterprise model route is not authorized")
	}
	capability := modelCapabilityForScenario(route.Scenario)
	if capability == "" {
		return nil, httperr.New(503, 50396, "enterprise model route capability is required")
	}
	authorizedCapability := false
	for _, allowedCapability := range decodeStringList(bindingVersion.AllowedCapabilities) {
		if allowedCapability == capability {
			authorizedCapability = true
			break
		}
	}
	if !authorizedCapability {
		return nil, httperr.New(503, 50396, "enterprise binding does not authorize this capability")
	}
	return pin, nil
}

func modelCapabilityForScenario(scenario string) string {
	scenario = strings.ToLower(strings.TrimSpace(scenario))
	switch scenario {
	case "chat", "completion", "embedding", "rerank":
		return scenario
	default:
		return ""
	}
}

// ModelRoutePinReference is the stable evidence identity stored in release
// snapshots: pin identity + immutable pin version.
func ModelRoutePinReference(pin *model.ModelRouteEnterprisePin) string {
	if pin == nil {
		return ""
	}
	return "pin:" + pin.PinID + ":v" + strconv.FormatInt(pin.Version, 10)
}
