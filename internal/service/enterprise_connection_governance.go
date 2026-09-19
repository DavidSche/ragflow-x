package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type RotateEnterpriseConnectionCredentialRequest struct {
	CredentialRef           string `json:"credential_ref"`
	CredentialVersion       string `json:"credential_version"`
	CredentialPolicyVersion string `json:"credential_policy_version"`
}

type enterpriseApprovalTarget struct {
	Connection *model.EnterpriseConnection
	Version    *model.EnterpriseConnectionVersion
	Binding    *model.EnterpriseConnectionBinding
	BindingVer *model.EnterpriseConnectionBindingVersion
	Snapshot   map[string]any
	Attrs      map[string]any
}

func (s *Service) enterpriseApprovalTargetSnapshot(target *enterpriseApprovalTarget) (map[string]any, map[string]any) {
	snapshot := map[string]any{
		"connection_id":             target.Connection.ID,
		"connection_version":        target.Connection.CurrentConnectionVersion,
		"connection_config_hash":    target.Version.ConnectionConfigHash,
		"credential_version":        target.Version.CredentialVersion,
		"credential_policy_version": target.Version.CredentialPolicyVersion,
		"lifecycle_status":          target.Connection.LifecycleStatus,
	}
	attrs := map[string]any{
		"connection_id":      target.Connection.ID,
		"connection_version": target.Connection.CurrentConnectionVersion,
		"credential_version": target.Version.CredentialVersion,
		"lifecycle_status":   target.Connection.LifecycleStatus,
	}
	if target.Binding != nil {
		snapshot["binding_id"] = target.Binding.BindingID
		snapshot["binding_version"] = target.Binding.CurrentBindingVersion
		snapshot["binding_tenant_id"] = target.Binding.TenantID
		snapshot["binding_lifecycle_status"] = target.Binding.LifecycleStatus
		attrs["binding_id"] = target.Binding.BindingID
		attrs["binding_version"] = target.Binding.CurrentBindingVersion
		attrs["binding_tenant_id"] = target.Binding.TenantID
	}
	return snapshot, attrs
}

func (s *Service) resolveEnterpriseConnectionApprovalTarget(ctx context.Context, actorID string, approval *model.Approval) (*enterpriseApprovalTarget, error) {
	if err := s.Authorize(ctx, actorID, "manage", "enterprise-connection"); err != nil {
		return nil, err
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	connectionID, _ := payload["connection_id"].(string)
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" && approval.ObjectType == model.ApprovalObjectEnterpriseConnection {
		connectionID = approval.ObjectID
	}
	if connectionID == "" {
		return nil, httperr.BadRequest(40068, "connection_id is required")
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	version, err := s.Store.GetEnterpriseConnectionVersion(ctx, connection.ID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
	}
	if connection.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.BadRequest(40070, "active enterprise connection is required")
	}
	target := &enterpriseApprovalTarget{Connection: connection, Version: version}
	if approval.Action == model.ApprovalActionRotateCredential {
		if connection.LifecycleStatus != model.EnterpriseLifecycleActive {
			return nil, httperr.BadRequest(40069, "active enterprise connection is required")
		}
		credentialVersion, _ := payload["credential_version"].(string)
		credentialVersion = strings.TrimSpace(credentialVersion)
		if credentialVersion == "" {
			return nil, httperr.BadRequest(40069, "credential_version is required")
		}
		if credentialVersion == version.CredentialVersion {
			return nil, httperr.BadRequest(40069, "credential_version must change")
		}
	}
	if approval.Action == model.ApprovalActionRetire {
		if connection.LifecycleStatus == model.EnterpriseLifecycleRetired {
			return nil, httperr.BadRequest(40069, "enterprise connection is already retired")
		}
		bindings, err := s.Store.ListEnterpriseBindingsByConnection(ctx, connection.ID)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			if binding.LifecycleStatus == model.EnterpriseLifecycleActive {
				return nil, httperr.BadRequest(40069, "active bindings must be revoked before retirement")
			}
		}
		routes, err := s.Store.ListModelRoutesByConnection(ctx, nil, connection.ID)
		if err != nil {
			return nil, err
		}
		if len(routes) > 0 {
			return nil, httperr.BadRequest(40069, "current model route pins must be migrated before retirement")
		}
	}
	target.Snapshot, target.Attrs = s.enterpriseApprovalTargetSnapshot(target)
	return target, nil
}

func (s *Service) resolveEnterpriseBindingApprovalTarget(ctx context.Context, actorID string, approval *model.Approval) (*enterpriseApprovalTarget, error) {
	if err := s.Authorize(ctx, actorID, "manage", "enterprise-connection"); err != nil {
		return nil, err
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	connectionID, _ := payload["connection_id"].(string)
	connectionID = strings.TrimSpace(connectionID)
	var bindingID string
	if approval.Action == model.ApprovalActionBind {
		var req CreateEnterpriseBindingRequest
		if err := decodeApprovalPayload(payload, &req); err != nil {
			return nil, httperr.BadRequest(40070, "invalid enterprise binding payload")
		}
		req.ConnectionID = strings.TrimSpace(req.ConnectionID)
		req.TenantID = strings.TrimSpace(req.TenantID)
		if req.ConnectionID == "" || req.TenantID == "" || len(req.AllowedModelRefs) == 0 {
			return nil, httperr.BadRequest(40070, "connection_id, tenant_id and allowed_model_refs are required")
		}
		tenant, tenantErr := s.Store.GetTenant(ctx, req.TenantID)
		if tenantErr != nil {
			return nil, tenantErr
		}
		if tenant == nil || tenant.Type != model.TenantTypeWorkspace {
			return nil, httperr.BadRequest(40070, "binding target must be a workspace tenant")
		}
		connectionID = req.ConnectionID
	} else {
		bindingID = approval.ObjectID
		if bindingID == "" {
			return nil, httperr.BadRequest(40070, "binding id is required")
		}
		binding, err := s.Store.GetEnterpriseBinding(ctx, bindingID)
		if err != nil {
			return nil, err
		}
		if binding == nil {
			return nil, httperr.NotFound("enterprise binding not found")
		}
		if connectionID == "" {
			connectionID = binding.ConnectionID
		}
		if connectionID != binding.ConnectionID {
			return nil, httperr.BadRequest(40070, "connection_id does not match binding")
		}
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	version, err := s.Store.GetEnterpriseConnectionVersion(ctx, connection.ID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
	}
	target := &enterpriseApprovalTarget{Connection: connection, Version: version}
	if approval.Action != model.ApprovalActionBind {
		binding, err := s.Store.GetEnterpriseBinding(ctx, bindingID)
		if err != nil {
			return nil, err
		}
		if binding == nil {
			return nil, httperr.NotFound("enterprise binding not found")
		}
		target.Binding = binding
		if approval.Action == model.ApprovalActionRevoke && binding.LifecycleStatus != model.EnterpriseLifecycleActive {
			return nil, httperr.BadRequest(40070, "binding is already revoked or disabled")
		}
		if approval.Action == model.ApprovalActionUpdate {
			if binding.LifecycleStatus != model.EnterpriseLifecycleActive {
				return nil, httperr.BadRequest(40070, "inactive binding cannot be edited")
			}
			var req UpdateEnterpriseBindingRequest
			if err := decodeApprovalPayload(payload, &req); err != nil {
				return nil, httperr.BadRequest(40070, "invalid enterprise binding payload")
			}
			if len(req.AllowedModelRefs) == 0 {
				return nil, httperr.BadRequest(40070, "allowed_model_refs is required")
			}
		}
		bindingVersion, err := s.Store.GetEnterpriseBindingVersion(ctx, binding.BindingID, binding.CurrentBindingVersion)
		if err != nil {
			return nil, err
		}
		if bindingVersion == nil {
			return nil, httperr.NotFound("enterprise binding version not found")
		}
		target.BindingVer = bindingVersion
	}
	target.Snapshot, target.Attrs = s.enterpriseApprovalTargetSnapshot(target)
	return target, nil
}

func (s *Service) RotateEnterpriseConnectionCredential(ctx context.Context, connectionID, actorID string, req RotateEnterpriseConnectionCredentialRequest) (*EnterpriseConnectionView, error) {
	req.CredentialRef = strings.TrimSpace(req.CredentialRef)
	req.CredentialVersion = strings.TrimSpace(req.CredentialVersion)
	req.CredentialPolicyVersion = strings.TrimSpace(req.CredentialPolicyVersion)
	if req.CredentialVersion == "" {
		return nil, httperr.BadRequest(40069, "credential_version is required")
	}
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	if connection.LifecycleStatus != model.EnterpriseLifecycleActive {
		return nil, httperr.BadRequest(40069, "active enterprise connection is required")
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.rotate_secret"); err != nil {
		return nil, err
	}
	current, err := s.Store.GetEnterpriseConnectionVersion(ctx, connectionID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
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
	if req.CredentialVersion == current.CredentialVersion {
		return nil, httperr.BadRequest(40069, "credential_version must change")
	}
	if req.CredentialRef == "" {
		req.CredentialRef = current.CredentialRef
	}
	if req.CredentialPolicyVersion == "" {
		req.CredentialPolicyVersion = current.CredentialPolicyVersion
	}
	if err := ValidateProviderBaseURL(current.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	next := *current
	next.Version = nextVersion
	next.CredentialRef = req.CredentialRef
	next.CredentialVersion = req.CredentialVersion
	next.CredentialPolicyVersion = req.CredentialPolicyVersion
	next.ConnectionConfigHash = enterpriseConnectionConfigHash(&next)
	next.CreatedAt = time.Now().UTC()
	if next.ConnectionConfigHash == current.ConnectionConfigHash {
		return nil, httperr.BadRequest(40069, "credential rotation payload did not change")
	}
	if err := s.Store.CreateEnterpriseConnectionDraftVersion(ctx, &next); err != nil {
		return nil, err
	}
	check, probeErr := s.probeEnterpriseConnectionVersion(ctx, connection, &next, actorID)
	if probeErr != nil || check.Health != model.RuntimeHealthHealthy {
		if probeErr == nil {
			probeErr = httperr.New(502, 50269, "credential rotation health validation failed")
		}
		check.ConnectionVersion = connection.CurrentConnectionVersion
		if recordErr := s.Store.RecordEnterpriseConnectionHealthCheck(ctx, check, check.Health, check.CreatedAt); recordErr != nil {
			return nil, recordErr
		}
		return nil, probeErr
	}
	check.ConnectionVersion = next.Version
	if err := s.Store.ActivateEnterpriseConnectionVersionWithHealthCheck(ctx, connection, &next, check); err != nil {
		return nil, err
	}
	connection.CurrentConnectionVersion = next.Version
	connection.RuntimeHealth = model.RuntimeHealthHealthy
	lastHealthCheckAt := check.CreatedAt
	connection.LastHealthCheckAt = &lastHealthCheckAt
	return &EnterpriseConnectionView{Connection: *connection, Version: next}, nil
}

func (s *Service) probeEnterpriseConnectionVersion(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion, actorID string) (*model.EnterpriseConnectionHealthCheck, error) {
	target, err := probeURL(version.BaseURL)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, enterpriseConnectionProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, httperr.BadRequest(40067, "enterprise connection probe URL is invalid")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RAGFlow-X-HealthProbe/1.0")
	started := time.Now().UTC()
	resp, err := s.enterpriseProbeClient().Do(req)
	latency := time.Since(started).Milliseconds()
	now := time.Now().UTC()
	check := &model.EnterpriseConnectionHealthCheck{
		ID: id.New(), ConnectionID: connection.ID, ConnectionVersion: version.Version,
		LatencyMS: latency, CreatedBy: actorID, CreatedAt: now,
	}
	if err != nil {
		check.Health = model.RuntimeHealthUnavailable
		check.Message = "provider endpoint is unreachable"
		return check, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
	check.HTTPStatus = resp.StatusCode
	check.Health, check.Message = enterpriseHealthFromHTTPStatus(resp.StatusCode)
	return check, nil
}

func (s *Service) RetireEnterpriseConnection(ctx context.Context, actorID, connectionID string) (*EnterpriseConnectionView, error) {
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	if connection.LifecycleStatus == model.EnterpriseLifecycleRetired {
		return nil, httperr.BadRequest(40069, "enterprise connection is already retired")
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.manage"); err != nil {
		return nil, err
	}
	bindings, err := s.Store.ListEnterpriseBindingsByConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.LifecycleStatus == model.EnterpriseLifecycleActive {
			return nil, httperr.BadRequest(40069, "active bindings must be revoked before retirement")
		}
	}
	routes, err := s.Store.ListModelRoutesByConnection(ctx, nil, connectionID)
	if err != nil {
		return nil, err
	}
	if len(routes) > 0 {
		return nil, httperr.BadRequest(40069, "current model route pins must be migrated before retirement")
	}
	retired, err := s.Store.RetireEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if !retired {
		return nil, httperr.New(409, 40969, "enterprise connection retirement state changed")
	}
	connection.LifecycleStatus = model.EnterpriseLifecycleRetired
	version, err := s.Store.GetEnterpriseConnectionVersion(ctx, connectionID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
	}
	return &EnterpriseConnectionView{Connection: *connection, Version: *version}, nil
}

func (s *Service) DeprecateEnterpriseConnection(ctx context.Context, actorID, connectionID string) (*EnterpriseConnectionView, error) {
	connection, err := s.Store.GetEnterpriseConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, httperr.NotFound("enterprise connection not found")
	}
	if connection.LifecycleStatus == model.EnterpriseLifecycleDeprecated {
		return nil, httperr.BadRequest(40069, "enterprise connection is already deprecated")
	}
	if err := s.authorizeEnterpriseConnectionOperation(ctx, actorID, connection, "tenant.connection.manage"); err != nil {
		return nil, err
	}
	changed, err := s.Store.SetEnterpriseConnectionLifecycle(ctx, connectionID, model.EnterpriseLifecycleDeprecated)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, httperr.New(409, 40969, "enterprise connection lifecycle state changed")
	}
	connection.LifecycleStatus = model.EnterpriseLifecycleDeprecated
	version, err := s.Store.GetEnterpriseConnectionVersion(ctx, connectionID, connection.CurrentConnectionVersion)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, httperr.NotFound("enterprise connection version not found")
	}
	return &EnterpriseConnectionView{Connection: *connection, Version: *version}, nil
}

func (s *Service) validateEnterpriseConnectionApproval(ctx context.Context, approval *model.Approval) error {
	actorID := approval.RequesterID
	var target *enterpriseApprovalTarget
	var err error
	if approval.ObjectType == model.ApprovalObjectEnterpriseBinding {
		target, err = s.resolveEnterpriseBindingApprovalTarget(ctx, actorID, approval)
	} else {
		target, err = s.resolveEnterpriseConnectionApprovalTarget(ctx, actorID, approval)
	}
	if err != nil {
		return err
	}
	return enterpriseApprovalSnapshotMatches(approval.SnapshotJSON, target.Snapshot)
}

func enterpriseApprovalSnapshotMatches(raw string, current map[string]any) error {
	stored := map[string]any{}
	if raw == "" || json.Unmarshal([]byte(raw), &stored) != nil {
		return httperr.BadRequest(40071, "approval resource snapshot is invalid")
	}
	for _, key := range []string{
		"connection_id", "connection_version", "connection_config_hash", "credential_version",
		"credential_policy_version", "lifecycle_status", "binding_id", "binding_version",
		"binding_tenant_id", "binding_lifecycle_status",
	} {
		approvedValue, approved := stored[key]
		if !approved {
			continue
		}
		currentValue, exists := current[key]
		if !exists || fmt.Sprint(approvedValue) != fmt.Sprint(currentValue) {
			return httperr.New(409, 40971, "approval resource version has changed")
		}
	}
	return nil
}

func (s *Service) authorizeEnterpriseConnectionOperation(ctx context.Context, actorID string, connection *model.EnterpriseConnection, operation string) error {
	if connection == nil {
		return httperr.NotFound("enterprise connection not found")
	}
	authz, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	if authz.user.TenantID == connection.OwnerTenantID &&
		connection.ManagedBy != model.EnterpriseConnectionManagedByPlatform &&
		connection.Visibility != model.EnterpriseConnectionVisibilityShared {
		return nil
	}
	return s.Authorize(ctx, actorID, operation, "tenant")
}

func (s *Service) executeEnterpriseBindingCreate(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req CreateEnterpriseBindingRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40070, "invalid enterprise binding payload")
	}
	req.ApprovalID = approval.ID
	view, err := s.CreateEnterpriseBinding(ctx, approval.RequesterID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Binding.ConnectionID, "binding_id": view.Binding.BindingID,
		"binding_version": view.Binding.CurrentBindingVersion,
	}, nil
}

func (s *Service) executeEnterpriseBindingUpdate(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req UpdateEnterpriseBindingRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40070, "invalid enterprise binding payload")
	}
	view, err := s.UpdateEnterpriseBinding(ctx, approval.RequesterID, approval.ObjectID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Binding.ConnectionID, "binding_id": view.Binding.BindingID,
		"binding_version": view.Binding.CurrentBindingVersion,
	}, nil
}

func (s *Service) executeEnterpriseBindingRevoke(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	connectionID, _ := payload["connection_id"].(string)
	binding, err := s.Store.GetEnterpriseBinding(ctx, approval.ObjectID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("enterprise binding not found")
	}
	if connectionID != "" && connectionID != binding.ConnectionID {
		return nil, httperr.BadRequest(40070, "connection_id does not match binding")
	}
	view, err := s.RevokeEnterpriseBinding(ctx, approval.RequesterID, binding.BindingID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Binding.ConnectionID, "binding_id": view.Binding.BindingID,
		"binding_version": view.Binding.CurrentBindingVersion, "lifecycle_status": view.Binding.LifecycleStatus,
	}, nil
}

func (s *Service) executeEnterpriseCredentialRotation(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req RotateEnterpriseConnectionCredentialRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40069, "invalid credential rotation payload")
	}
	view, err := s.RotateEnterpriseConnectionCredential(ctx, approval.ObjectID, approval.RequesterID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Connection.ID, "connection_version": view.Connection.CurrentConnectionVersion,
		"connection_config_hash": view.Version.ConnectionConfigHash,
		"credential_version":     view.Version.CredentialVersion,
	}, nil
}

func (s *Service) executeEnterpriseConnectionRetire(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	view, err := s.RetireEnterpriseConnection(ctx, approval.RequesterID, approval.ObjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Connection.ID, "connection_version": view.Connection.CurrentConnectionVersion,
		"lifecycle_status": view.Connection.LifecycleStatus,
	}, nil
}

func (s *Service) executeEnterpriseConnectionDeprecate(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	view, err := s.DeprecateEnterpriseConnection(ctx, approval.RequesterID, approval.ObjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id":      view.Connection.ID,
		"connection_version": view.Connection.CurrentConnectionVersion,
		"lifecycle_status":   view.Connection.LifecycleStatus,
	}, nil
}

func (s *Service) executeEnterpriseConnectionCreate(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req CreateEnterpriseConnectionRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40060, "invalid enterprise connection payload")
	}
	view, err := s.CreateEnterpriseConnection(ctx, approval.RequesterID, approval.TenantID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Connection.ID, "connection_version": view.Connection.CurrentConnectionVersion,
		"connection_config_hash": view.Version.ConnectionConfigHash,
	}, nil
}

func (s *Service) executeEnterpriseConnectionUpdate(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req UpdateEnterpriseConnectionRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40060, "invalid enterprise connection payload")
	}
	view, err := s.UpdateEnterpriseConnection(ctx, approval.RequesterID, approval.ObjectID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"connection_id": view.Connection.ID, "connection_version": view.Connection.CurrentConnectionVersion,
		"connection_config_hash": view.Version.ConnectionConfigHash,
	}, nil
}

func enterpriseApprovalExecutorKey(objectType, action string) string {
	return fmt.Sprintf("%s.%s", objectType, action)
}

func validateEnterpriseConnectionApproval(ctx context.Context, svc *Service, approval *model.Approval) error {
	return svc.validateEnterpriseConnectionApproval(ctx, approval)
}

func executeEnterpriseBindingCreate(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseBindingCreate(ctx, approval)
}

func executeEnterpriseBindingUpdate(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseBindingUpdate(ctx, approval)
}

func executeEnterpriseBindingRevoke(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseBindingRevoke(ctx, approval)
}

func executeEnterpriseCredentialRotation(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseCredentialRotation(ctx, approval)
}

func executeEnterpriseConnectionRetire(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseConnectionRetire(ctx, approval)
}

func executeEnterpriseConnectionDeprecate(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseConnectionDeprecate(ctx, approval)
}

func executeEnterpriseConnectionCreate(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseConnectionCreate(ctx, approval)
}

func executeEnterpriseConnectionUpdate(ctx context.Context, svc *Service, approval *model.Approval) (map[string]any, error) {
	return svc.executeEnterpriseConnectionUpdate(ctx, approval)
}

func encodeApprovalPayload(payload map[string]any) string {
	raw, _ := json.Marshal(payload)
	return string(raw)
}
