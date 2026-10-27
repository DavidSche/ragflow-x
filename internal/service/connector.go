package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/connectorid"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/strutil"
	connectorpkg "github.com/ragflow-x/ragflow-x/internal/provider/connector"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Connector control-plane error codes (doc/225 §7.2, segment 40330–40340).
const (
	errConnectorNotFound          = "connector not found"
	errConnectorProviderUnsup     = "unsupported connector provider"
	errConnectorSpecInvalid       = "invalid connector spec"
	errConnectorScheduleInvalid   = "invalid connector schedule"
	errConnectorDisabled          = "connector is disabled"
	errConnectorUnreachable       = "connector source unreachable"
	errConnectorCredentialFailed  = "connector credential decryption failed"
	errConnectorActiveRun         = "connector already has an active run"
	errConnectorTargetNotWritable = "target dataset is not writable"
	errConnectorStillReferenced   = "connector still has an active run; disable it first"
	errConnectorPreviewLimit      = "connector preview limit exceeded"
)

// connectorUploader is the governance ingestion actor recorded on document
// uploads performed by a scheduled connector run.
const connectorUploader = "system:connector"

// connectorActorSystem labels runs triggered by the scheduler rather than a
// human operator.
const connectorActorSystem = "system"

// ConnectorCreateInput is the validated connector control-plane payload.
type ConnectorCreateInput struct {
	Name                string
	Provider            string
	AdapterKind         string
	ExternalConnectorID string
	TargetDatasetID     string
	SpecJSON            string
	CredentialJSON      string // plaintext JSON of string KV values
	ScheduleKind        string
	ScheduleValue       int
	ScheduleTZOffsetMin int
	Sensitive           bool
	Enabled             *bool
	CreatedBy           string
}

// ConnectorUpdateInput overlays mutable fields on an existing connector.
type ConnectorUpdateInput struct {
	Name                string
	Provider            string
	AdapterKind         string
	ExternalConnectorID string
	TargetDatasetID     string
	SpecJSON            string
	CredentialJSON      string // empty => keep existing sealed credential
	ScheduleKind        string
	ScheduleValue       int
	ScheduleTZOffsetMin int
	ScheduleTZOffsetSet bool
	Sensitive           *bool
	Enabled             *bool
}

// ConnectorTestInput drives a connectivity probe without persisting.
type ConnectorTestInput struct {
	Provider       string
	SpecJSON       string
	CredentialJSON string
}

// ConnectorHealth mirrors the enterprise-connection probe semantics
// (doc/225 §7.1a: reachable/auth_ok/err_msg).
type ConnectorHealth struct {
	Reachable bool      `json:"reachable"`
	AuthOK    bool      `json:"auth_ok"`
	ErrMsg    string    `json:"err_msg,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// ConnectorPreviewEntry is one read-only diff row of a preview run.
type ConnectorPreviewEntry struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Revision string `json:"revision"`
	Change   string `json:"change"` // added | changed | deleted | unchanged
}

// ConnectorPreview is the dry-run response; it never writes documents or the
// durable cursor (doc/225 §7.2: preview ≤50 entries).
type ConnectorPreview struct {
	Entries        []ConnectorPreviewEntry `json:"entries"`
	Added          int                     `json:"added"`
	Changed        int                     `json:"changed"`
	Deleted        int                     `json:"deleted"`
	Skipped        int                     `json:"skipped"`
	CursorReturned string                  `json:"next_cursor,omitempty"`
}

func (s *Service) connectorProviders() *connectorpkg.Registry {
	if s.connectorRegistry == nil {
		s.connectorRegistry = connectorpkg.NewRegistry()
	}
	return s.connectorRegistry
}

// SetConnectorConfig installs the runtime tunables (doc/225 §6.2). The
// concurrency semaphore is process-local; multi-replica deployments keep it
// small because each replica enforces its own share.
func (s *Service) SetConnectorConfig(cfg config.Connector) {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = model.ConnectorDefaultMaxConcurrent
	}
	if cfg.RunTimeoutSec <= 0 {
		cfg.RunTimeoutSec = model.ConnectorDefaultRunTimeoutSec
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = model.ConnectorRetentionDefaultDays
	}
	if cfg.PreviewLimit <= 0 {
		cfg.PreviewLimit = 20
	}
	if cfg.PreviewLimit > 50 {
		cfg.PreviewLimit = 50
	}
	if cfg.MaxObjectMB <= 0 || cfg.MaxObjectMB > 25 {
		// Never exceed the document-upload ceiling enforced downstream (25 MiB).
		cfg.MaxObjectMB = 25
	}
	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	s.connectorConfig = cfg
	s.connectorSem = make(chan struct{}, cfg.MaxConcurrent)
}

// connectorSettings returns the effective config and the process-wide
// semaphore, initializing defaults on first use (tests construct Service
// directly without going through setup).
func (s *Service) connectorSettings() (config.Connector, chan struct{}) {
	s.connectorMu.Lock()
	defer s.connectorMu.Unlock()
	if s.connectorConfig.MaxConcurrent <= 0 {
		s.connectorConfig = config.Connector{
			MaxConcurrent: model.ConnectorDefaultMaxConcurrent,
			RunTimeoutSec: model.ConnectorDefaultRunTimeoutSec,
			RetentionDays: model.ConnectorRetentionDefaultDays,
			PreviewLimit:  20,
			MaxObjectMB:   25,
		}
	}
	if s.connectorSem == nil {
		s.connectorSem = make(chan struct{}, s.connectorConfig.MaxConcurrent)
	}
	return s.connectorConfig, s.connectorSem
}

func parseConnectorSpec(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func parseConnectorCred(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func parseConnectorCursor(raw string) map[string]string {
	m := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return m
	}
	// tolerant: any parse failure resets to an empty cursor
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

func mustSpecJSON(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// normalizeConnectorAdapter defaults legacy/unspecified payloads to the native
// adapter so existing clients keep working unchanged.
func normalizeConnectorAdapter(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return model.ConnectorAdapterNative
	}
	return kind
}

// validateConnectorAdapter fails closed for adapters whose remote semantics
// cannot honour X's business loop yet (preview, manual incremental trigger,
// run status/cursor). Reserving the value keeps the schema forward-compatible
// without exposing a half-closed connector (doc/227 C2.3/D3).
func validateConnectorAdapter(kind, externalID string) error {
	switch kind {
	case model.ConnectorAdapterNative:
		if strings.TrimSpace(externalID) != "" {
			return httperr.BadRequest(40333, "external_connector_id is only valid for a ragflow-backed adapter")
		}
		return nil
	case model.ConnectorAdapterRAGFlow:
		return httperr.BadRequest(40333, "ragflow-backed connector adapter is not available in this release")
	default:
		return httperr.BadRequest(40333, "unsupported connector adapter")
	}
}

// connectorSourceOwnershipKey delegates to the shared canonical identity so
// the migration and runtime can never drift (doc/227 C2.3).
func connectorSourceOwnershipKey(tenantID, provider, datasetID string, spec map[string]any) string {
	return connectorid.OwnershipKey(tenantID, provider, datasetID, spec)
}

// connectorSourceTaken also compares legacy rows whose ownership key predates
// migration 156. Without this scan, deleting the canonical binding would let a
// duplicate legacy binding coexist with a newly created canonical binding.
func (s *Service) connectorSourceTaken(ctx context.Context, tenantID, datasetID, provider, key, excludeID string) (bool, error) {
	rows, err := s.Store.ListConnectorsForDataset(ctx, tenantID, datasetID)
	if err != nil {
		return false, err
	}
	for i := range rows {
		row := &rows[i]
		if row.ID == excludeID || row.Provider != provider {
			continue
		}
		if row.SourceOwnershipKey == key {
			return true, nil
		}
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.SpecJSON), &spec); err != nil || spec == nil {
			continue
		}
		if connectorSourceOwnershipKey(tenantID, provider, datasetID, spec) == key {
			return true, nil
		}
	}
	return false, nil
}

// isConnectorOwnershipConflict maps the database unique constraint onto the
// documented immutable-identity error instead of leaking driver text. The
// normalized GORM duplicate-key error (PostgreSQL 23505, MySQL 1062) is the
// primary signal; the message fallback keeps drivers or versions that surface
// raw error text mapped instead of leaking driver text.
func isConnectorOwnershipConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "uk_connector_ownership") ||
		strings.Contains(msg, "unique") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "23505") ||
		strings.Contains(msg, "1062")
}

func validateConnectorSchedule(kind string, value int) error {
	switch kind {
	case model.ConnectorScheduleManual, model.ConnectorScheduleInterval, model.ConnectorScheduleDaily:
	case "":
		kind = model.ConnectorScheduleManual
	default:
		return fmt.Errorf("unsupported schedule kind %q", kind)
	}
	switch kind {
	case model.ConnectorScheduleInterval:
		if value < 5 {
			return fmt.Errorf("interval must be >= 5 minutes")
		}
	case model.ConnectorScheduleDaily:
		if value < 0 || value > 1439 {
			return fmt.Errorf("daily time (UTC minute-of-day) must be in 0..1439")
		}
	}
	return nil
}

// ListConnectors returns a page of connector bindings for the scope.
func (s *Service) ListConnectors(ctx context.Context, scopeAll bool, tenantIDs []string, status, datasetID string, page, pageSize int) ([]model.Connector, int64, error) {
	list, total, err := s.Store.ListConnectorsForScope(ctx, scopeAll, tenantIDs, status, datasetID, page, pageSize)
	if err == nil {
		obs.Get().SetConnectorActiveConnectors(total)
	}
	return list, total, err
}

// ListConnectorRuns returns a page of run history for the scope.
func (s *Service) ListConnectorRuns(ctx context.Context, scopeAll bool, tenantIDs []string, connectorID string, page, pageSize int) ([]model.ConnectorRun, int64, error) {
	return s.Store.ListConnectorRunsForScope(ctx, scopeAll, tenantIDs, connectorID, page, pageSize)
}

// GetConnectorRun returns one run row scoped to the caller's connectors.
func (s *Service) GetConnectorRun(ctx context.Context, scopeAll bool, tenantIDs []string, connectorID, runID string) (*model.ConnectorRun, error) {
	run, err := s.Store.GetConnectorRunForScope(ctx, scopeAll, tenantIDs, connectorID, runID)
	if err != nil {
		return nil, httperr.NotFound("connector run not found")
	}
	if run == nil {
		return nil, httperr.NotFound("connector run not found")
	}
	return run, nil
}

// GetConnectorForScope returns one connector with credentials sealed.
func (s *Service) GetConnectorForScope(ctx context.Context, scopeAll bool, tenantIDs []string, id string) (*model.Connector, error) {
	c, err := s.Store.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, httperr.New(404, 40330, errConnectorNotFound)
	}
	return c, nil
}

// ConnectorProvidersMeta returns UI metadata for all registered providers.
func (s *Service) ConnectorProvidersMeta() []connectorpkg.Meta {
	r := s.connectorProviders()
	out := make([]connectorpkg.Meta, 0, len(r.Types()))
	for _, t := range r.Types() {
		m, _ := r.MetaFor(t)
		m.Type = t
		out = append(out, m)
	}
	return out
}

// TestConnector probes connectivity for the given provider/spec/credential.
func (s *Service) TestConnector(ctx context.Context, in ConnectorTestInput) error {
	p := s.connectorProviders().New(in.Provider)
	if p == nil {
		return httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	spec, err := parseConnectorSpec(in.SpecJSON)
	if err != nil {
		return httperr.BadRequest(40333, errConnectorSpecInvalid+": "+err.Error())
	}
	if err := p.ValidateSpec(spec); err != nil {
		return httperr.BadRequest(40333, errConnectorSpecInvalid+": "+err.Error())
	}
	cred, err := parseConnectorCred(in.CredentialJSON)
	if err != nil {
		return httperr.BadRequest(40333, "invalid connector credential: "+err.Error())
	}
	if err := p.Test(ctx, spec, cred); err != nil {
		return httperr.New(502, 40332, errConnectorUnreachable+": "+err.Error())
	}
	return nil
}

// TestConnectorByID probes a persisted connector with its sealed credential
// (doc/225 §7.2: POST /connectors/:id/test is read-only).
func (s *Service) TestConnectorByID(ctx context.Context, scopeAll bool, tenantIDs []string, id string) (*ConnectorHealth, error) {
	c, err := s.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return nil, err
	}
	health := &ConnectorHealth{CheckedAt: time.Now().UTC()}
	p := s.connectorProviders().New(c.Provider)
	if p == nil {
		return health, httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	spec, serr := parseConnectorSpec(c.SpecJSON)
	if serr != nil {
		return health, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+serr.Error())
	}
	cred, cerr := s.connectorCredential(ctx, c)
	if cerr != nil {
		return health, cerr
	}
	if terr := p.Test(ctx, spec, cred); terr != nil {
		health.ErrMsg = terr.Error()
		s.recordConnectorAudit(ctx, c.TenantID, "connector.test", c.ID, health.ErrMsg)
		return health, httperr.New(502, 40332, errConnectorUnreachable+": "+terr.Error())
	}
	health.Reachable = true
	health.AuthOK = true
	s.recordConnectorAudit(ctx, c.TenantID, "connector.test", c.ID, "")
	return health, nil
}

// CreateConnector validates and persists a new connector binding (called after
// approval when the governance policy requires one).
func (s *Service) CreateConnector(ctx context.Context, tenantID string, in ConnectorCreateInput) (*model.Connector, error) {
	p, spec, err := s.validateNewConnectorSpec(in.Provider, in.SpecJSON)
	if err != nil {
		return nil, err
	}
	adapterKind := normalizeConnectorAdapter(in.AdapterKind)
	if err := validateConnectorAdapter(adapterKind, in.ExternalConnectorID); err != nil {
		return nil, err
	}
	schedule := in.ScheduleKind
	if schedule == "" {
		schedule = model.ConnectorScheduleManual
	}
	if err := validateConnectorSchedule(schedule, in.ScheduleValue); err != nil {
		return nil, httperr.BadRequest(40335, errConnectorScheduleInvalid+": "+err.Error())
	}
	if err := s.validateConnectorTarget(ctx, tenantID, in.TargetDatasetID); err != nil {
		return nil, err
	}
	credJSON, hasCred, credMap, err := s.sealConnectorCredential(in.CredentialJSON)
	if err != nil {
		return nil, err
	}
	sourceKey := connectorSourceOwnershipKey(tenantID, in.Provider, in.TargetDatasetID, spec)
	if taken, terr := s.connectorSourceTaken(ctx, tenantID, in.TargetDatasetID, in.Provider, sourceKey, ""); terr != nil {
		return nil, terr
	} else if taken {
		return nil, httperr.New(409, 40331, "connector source is already bound to another connector in this tenant and dataset")
	}
	// Fail closed on a credential/spec combination the provider rejects.
	if hasCred || p.Type() != model.ConnectorProviderLocalFolder {
		if terr := p.Test(ctx, spec, credMap); terr != nil {
			return nil, httperr.New(502, 40332, errConnectorUnreachable+": "+terr.Error())
		}
	}
	c := &model.Connector{
		ID:                  id.New(),
		TenantID:            tenantID,
		Name:                strings.TrimSpace(in.Name),
		Provider:            in.Provider,
		AdapterKind:         adapterKind,
		SourceOwnershipKey:  sourceKey,
		ExternalConnectorID: strings.TrimSpace(in.ExternalConnectorID),
		TargetDatasetID:     in.TargetDatasetID,
		SpecJSON:            mustSpecJSON(spec),
		CredentialCrypted:   credJSON,
		HasCredential:       hasCred,
		CredentialVersion:   0,
		CursorJSON:          "{}",
		ScheduleKind:        schedule,
		ScheduleValue:       in.ScheduleValue,
		ScheduleTZOffsetMin: in.ScheduleTZOffsetMin,
		Sensitive:           in.Sensitive,
		Enabled:             true,
		Status:              model.ConnectorStatusIdle,
		CreatedBy:           strings.TrimSpace(in.CreatedBy),
	}
	if c.CreatedBy == "" {
		c.CreatedBy = connectorActorSystem
	}
	if hasCred {
		c.CredentialVersion = 1
	}
	if in.Enabled != nil && !*in.Enabled {
		c.Enabled = false
		c.Status = model.ConnectorStatusDisabled
	}
	if c.Name == "" {
		return nil, httperr.BadRequest(40000, "connector name is required")
	}
	if err := s.Store.CreateConnector(ctx, c); err != nil {
		if isConnectorOwnershipConflict(err) {
			return nil, httperr.New(409, 40331, "connector source is already bound to another connector in this tenant and dataset")
		}
		return nil, err
	}
	if _, serr := s.ScheduleConnectorNext(ctx, c.TenantID, c.ID, ""); serr != nil {
		slog.Warn("connector initial schedule failed", "connector_id", c.ID, "error", serr)
	}
	s.recordConnectorAudit(ctx, c.TenantID, "connector.create", c.ID, "")
	return c, nil
}

func (s *Service) validateNewConnectorSpec(provider, specJSON string) (connectorpkg.Provider, map[string]any, error) {
	p := s.connectorProviders().New(provider)
	if p == nil {
		return nil, nil, httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	spec, err := parseConnectorSpec(specJSON)
	if err != nil {
		return nil, nil, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+err.Error())
	}
	if err := p.ValidateSpec(spec); err != nil {
		return nil, nil, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+err.Error())
	}
	return p, spec, nil
}

func (s *Service) sealConnectorCredential(raw string) (crypted string, has bool, plain map[string]string, err error) {
	if strings.TrimSpace(raw) == "" {
		return "", false, map[string]string{}, nil
	}
	plain, perr := parseConnectorCred(raw)
	if perr != nil {
		return "", false, nil, httperr.BadRequest(40333, "invalid connector credential: "+perr.Error())
	}
	sealed, cerr := crypto.Encrypt(s.EncryptKey, raw)
	if cerr != nil {
		return "", false, nil, httperr.Internal("connector credential seal failed")
	}
	return sealed, true, plain, nil
}

// validateConnectorTarget enforces target ownership: a connector may only feed
// a dataset in its own tenant (doc/225 §7.2: 40338 cross-tenant/archived).
func (s *Service) validateConnectorTarget(ctx context.Context, tenantID, datasetID string) error {
	if strings.TrimSpace(datasetID) == "" {
		return httperr.BadRequest(40000, "target_dataset_id is required")
	}
	d, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if d == nil || d.TenantID != tenantID {
		return httperr.BadRequest(40338, errConnectorTargetNotWritable)
	}
	return nil
}

// UpdateConnector overlays mutable fields and re-arms the schedule.
func (s *Service) UpdateConnector(ctx context.Context, scopeAll bool, tenantIDs []string, id string, in ConnectorUpdateInput) (*model.Connector, error) {
	c, err := s.Store.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, httperr.New(404, 40330, errConnectorNotFound)
	}
	c.AdapterKind = normalizeConnectorAdapter(c.AdapterKind)
	if err := validateConnectorAdapter(c.AdapterKind, c.ExternalConnectorID); err != nil {
		return nil, err
	}
	if in.Provider != "" && in.Provider != c.Provider {
		return nil, httperr.BadRequest(40331, "connector provider is immutable")
	}
	if in.AdapterKind != "" && normalizeConnectorAdapter(in.AdapterKind) != normalizeConnectorAdapter(c.AdapterKind) {
		return nil, httperr.BadRequest(40331, "connector adapter is immutable")
	}
	if in.ExternalConnectorID != "" && in.ExternalConnectorID != c.ExternalConnectorID {
		return nil, httperr.BadRequest(40331, "external connector id is immutable")
	}
	if strings.TrimSpace(in.Name) != "" {
		c.Name = strings.TrimSpace(in.Name)
	}
	sourceChanged := false
	if in.TargetDatasetID != "" && in.TargetDatasetID != c.TargetDatasetID {
		if err := s.validateConnectorTarget(ctx, c.TenantID, in.TargetDatasetID); err != nil {
			return nil, err
		}
		c.TargetDatasetID = in.TargetDatasetID
		sourceChanged = true
	}
	p := s.connectorProviders().New(c.Provider)
	if p == nil {
		return nil, httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	spec, _ := parseConnectorSpec(c.SpecJSON)
	specChanged := false
	if in.SpecJSON != "" {
		next, perr := parseConnectorSpec(in.SpecJSON)
		if perr != nil {
			return nil, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+perr.Error())
		}
		if verr := p.ValidateSpec(next); verr != nil {
			return nil, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+verr.Error())
		}
		spec = next
		c.SpecJSON = mustSpecJSON(next)
		specChanged = true
		sourceChanged = true
	}
	if in.ScheduleKind != "" {
		if err := validateConnectorSchedule(in.ScheduleKind, in.ScheduleValue); err != nil {
			return nil, httperr.BadRequest(40335, errConnectorScheduleInvalid+": "+err.Error())
		}
		c.ScheduleKind = in.ScheduleKind
		c.ScheduleValue = in.ScheduleValue
		if in.ScheduleTZOffsetSet {
			c.ScheduleTZOffsetMin = in.ScheduleTZOffsetMin
		}
	}
	if in.Sensitive != nil {
		c.Sensitive = *in.Sensitive
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
		if *in.Enabled {
			if c.Status == model.ConnectorStatusDisabled {
				c.Status = model.ConnectorStatusIdle
			}
		} else {
			c.Status = model.ConnectorStatusDisabled
		}
	} else if !c.Enabled {
		c.Status = model.ConnectorStatusDisabled
	}
	credChanged := false
	cred, cerr := s.connectorCredential(ctx, c)
	if cerr != nil {
		return nil, cerr
	}
	if strings.TrimSpace(in.CredentialJSON) != "" {
		sealed, has, plain, serr := s.sealConnectorCredential(in.CredentialJSON)
		if serr != nil {
			return nil, serr
		}
		c.CredentialCrypted = sealed
		c.HasCredential = has
		c.CredentialVersion++
		if c.CredentialVersion < 1 {
			c.CredentialVersion = 1
		}
		cred = plain
		credChanged = true
	}
	// Changing credentials or the source spec forces a fresh connectivity probe
	// (doc/225 §7.2: credential rotation must be re-tested before saving).
	if credChanged || specChanged {
		if terr := p.Test(ctx, spec, cred); terr != nil {
			return nil, httperr.New(502, 40332, errConnectorUnreachable+": "+terr.Error())
		}
	}
	if sourceChanged {
		sourceKey := connectorSourceOwnershipKey(c.TenantID, c.Provider, c.TargetDatasetID, spec)
		if taken, terr := s.connectorSourceTaken(ctx, c.TenantID, c.TargetDatasetID, c.Provider, sourceKey, c.ID); terr != nil {
			return nil, terr
		} else if taken {
			return nil, httperr.New(409, 40331, "connector source is already bound to another connector in this tenant and dataset")
		}
		c.SourceOwnershipKey = sourceKey
	}
	ok, uerr := s.Store.UpdateConnector(ctx, c)
	if uerr != nil {
		if isConnectorOwnershipConflict(uerr) {
			return nil, httperr.New(409, 40331, "connector source is already bound to another connector in this tenant and dataset")
		}
		return nil, uerr
	}
	if !ok {
		return nil, httperr.New(404, 40330, errConnectorNotFound)
	}
	if c.Status == model.ConnectorStatusDisabled {
		s.cancelConnectorScheduledJobs(ctx, c.TenantID, c.ID)
	} else if _, serr := s.ScheduleConnectorNext(ctx, c.TenantID, c.ID, ""); serr != nil {
		slog.Warn("connector reschedule failed", "connector_id", c.ID, "error", serr)
	}
	action := "connector.update"
	if credChanged {
		action = "connector.credential_rotate"
	}
	s.recordConnectorAudit(ctx, c.TenantID, action, c.ID, "")
	return c, nil
}

// DeleteConnector removes the binding; run history is retained (doc/225 F4).
func (s *Service) DeleteConnector(ctx context.Context, scopeAll bool, tenantIDs []string, id string) error {
	c, err := s.Store.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return err
	}
	if c == nil {
		return httperr.New(404, 40330, errConnectorNotFound)
	}
	if n, cerr := s.Store.ListActiveConnectorRuns(ctx, c.ID); cerr != nil {
		return cerr
	} else if n > 0 {
		return httperr.New(409, 40339, errConnectorStillReferenced)
	}
	if _, derr := s.Store.DeleteConnector(ctx, c.TenantID, id); derr != nil {
		return derr
	}
	s.cancelConnectorScheduledJobs(ctx, c.TenantID, c.ID)
	s.recordConnectorAudit(ctx, c.TenantID, "connector.delete", id, "")
	return nil
}

// SetConnectorEnabled toggles the disabled state and re-arms the schedule on
// enable. Disabling cancels queued scheduled runs so a disabled connector does
// not fire until re-enabled (doc/225 §7.2).
func (s *Service) SetConnectorEnabled(ctx context.Context, scopeAll bool, tenantIDs []string, id string, disabled bool) error {
	c, err := s.Store.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return err
	}
	if c == nil {
		return httperr.New(404, 40330, errConnectorNotFound)
	}
	ok, uerr := s.Store.SetConnectorEnabled(ctx, c.TenantID, id, disabled)
	if uerr != nil {
		return uerr
	}
	if !ok {
		return httperr.New(404, 40330, errConnectorNotFound)
	}
	if disabled {
		s.cancelConnectorScheduledJobs(ctx, c.TenantID, id)
	} else if _, serr := s.ScheduleConnectorNext(ctx, c.TenantID, id, ""); serr != nil {
		slog.Warn("connector enable schedule failed", "connector_id", id, "error", serr)
	}
	action := "connector.disable"
	if !disabled {
		action = "connector.enable"
	}
	s.recordConnectorAudit(ctx, c.TenantID, action, id, "")
	return nil
}

// SetConnectorSchedule changes the period and re-arms the recurring job.
func (s *Service) SetConnectorSchedule(ctx context.Context, scopeAll bool, tenantIDs []string, id, kind string, value, tzOffset int) (*model.Connector, error) {
	if err := validateConnectorSchedule(kind, value); err != nil {
		return nil, httperr.BadRequest(40335, errConnectorScheduleInvalid+": "+err.Error())
	}
	c, err := s.Store.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, httperr.New(404, 40330, errConnectorNotFound)
	}
	c.ScheduleKind = kind
	c.ScheduleValue = value
	if kind == model.ConnectorScheduleManual {
		c.ScheduleValue = 0
	}
	c.ScheduleTZOffsetMin = tzOffset
	ok, uerr := s.Store.UpdateConnector(ctx, c)
	if uerr != nil {
		return nil, uerr
	}
	if !ok {
		return nil, httperr.New(404, 40330, errConnectorNotFound)
	}
	s.cancelConnectorScheduledJobs(ctx, c.TenantID, c.ID)
	if _, serr := s.ScheduleConnectorNext(ctx, c.TenantID, c.ID, ""); serr != nil {
		slog.Warn("connector schedule update failed", "connector_id", c.ID, "error", serr)
	}
	s.recordConnectorAudit(ctx, c.TenantID, "connector.schedule", c.ID, kind)
	return c, nil
}

func (s *Service) cancelConnectorScheduledJobs(ctx context.Context, tenantID, connectorID string) {
	if s.Runner == nil {
		return
	}
	if _, err := s.Store.CancelQueuedJobsWithPayload(ctx, model.JobKindConnectorSync, tenantID, map[string]interface{}{
		"connector_id": connectorID,
		"trigger":      model.ConnectorRunTriggerScheduled,
	}); err != nil {
		slog.Warn("connector scheduled job cancel failed", "connector_id", connectorID, "error", err)
	}
}

// PreviewConnector dry-runs one page of a connector and reports the diff
// without uploading documents or advancing the durable cursor (doc/225 §7.2).
func (s *Service) PreviewConnector(ctx context.Context, scopeAll bool, tenantIDs []string, id string, limit int) (*ConnectorPreview, error) {
	cfg, _ := s.connectorSettings()
	if limit <= 0 {
		limit = cfg.PreviewLimit
	}
	if limit > 50 {
		return nil, httperr.BadRequest(40337, errConnectorPreviewLimit)
	}
	c, err := s.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return nil, err
	}
	p := s.connectorProviders().New(c.Provider)
	if p == nil {
		return nil, httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	spec, serr := parseConnectorSpec(c.SpecJSON)
	if serr != nil {
		return nil, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+serr.Error())
	}
	cred, cerr := s.connectorCredential(ctx, c)
	if cerr != nil {
		return nil, cerr
	}
	entries, next, lerr := p.List(ctx, spec, cred, "")
	if lerr != nil {
		return nil, httperr.New(502, 40332, errConnectorUnreachable+": "+lerr.Error())
	}
	cursor := parseConnectorCursor(c.CursorJSON)
	out := &ConnectorPreview{Entries: make([]ConnectorPreviewEntry, 0, limit), CursorReturned: next}
	seen := map[string]struct{}{}
	for _, e := range entries {
		if len(out.Entries) >= limit {
			break
		}
		seen[e.Key] = struct{}{}
		change := "added"
		if old, ok := cursor[e.Key]; ok {
			if old == e.Revision {
				change = "unchanged"
				out.Skipped++
			} else {
				change = "changed"
				out.Changed++
			}
		} else {
			out.Added++
		}
		out.Entries = append(out.Entries, ConnectorPreviewEntry{
			Key: e.Key, Kind: e.Kind, Size: e.Size, Revision: e.Revision, Change: change,
		})
	}
	// Only report deletions when the provider scan was exhausted; otherwise the
	// unscanned tail would look deleted.
	if next == "" {
		for key := range cursor {
			if _, ok := seen[key]; ok {
				continue
			}
			out.Deleted++
			if len(out.Entries) < limit {
				out.Entries = append(out.Entries, ConnectorPreviewEntry{Key: key, Change: "deleted"})
			}
		}
	}
	return out, nil
}

// EnqueueConnectorManualTrigger runs a connector sync immediately (via the
// durable job queue) on manual request. Returns false when a run is already
// queued so manual and scheduled runs cannot pile up (doc/225 F6).
func (s *Service) EnqueueConnectorManualTrigger(ctx context.Context, scopeAll bool, tenantIDs []string, id, actor string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	c, err := s.Store.GetConnectorForScope(ctx, scopeAll, tenantIDs, id)
	if err != nil {
		return false, err
	}
	if c == nil {
		return false, httperr.New(404, 40330, errConnectorNotFound)
	}
	if !connectorSchedulingEnabled(c) {
		return false, httperr.BadRequest(40334, errConnectorDisabled)
	}
	if n, cerr := s.Store.ListActiveConnectorRuns(ctx, c.ID); cerr != nil {
		return false, cerr
	} else if n > 0 {
		return false, httperr.New(409, 40336, errConnectorActiveRun)
	}
	if n, cerr := s.Store.CountActiveJobsWithPayload(ctx, model.JobKindConnectorSync, c.TenantID, "", map[string]interface{}{"connector_id": c.ID}); cerr != nil {
		return false, cerr
	} else if n > 0 {
		return false, httperr.New(409, 40336, errConnectorActiveRun)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = connectorActorSystem
	}
	payload, _ := json.Marshal(map[string]string{
		"connector_id": c.ID, "trigger": model.ConnectorRunTriggerManual, "user_id": actor,
	})
	queued, qerr := s.Runner.Enqueue(ctx, model.JobKindConnectorSync, "connector_sync:"+c.ID+":manual:"+time.Now().UTC().Format("20060102T150405Z"), c.TenantID, string(payload), time.Time{}, 0)
	if qerr == nil && queued {
		s.recordConnectorAudit(ctx, c.TenantID, "connector.sync", c.ID, "manual")
	}
	return queued, qerr
}

// ScheduleConnectorNext arms (or re-arms) one recurring sync job for a
// non-manual connector. It never keeps two active sync jobs for the same
// connector, and a finishing worker excludes its own claim via excludeJobID so
// re-arming is not blocked (doc/225 §6.2).
func (s *Service) ScheduleConnectorNext(ctx context.Context, tenantID, connectorID, excludeJobID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	c, err := s.Store.GetConnector(ctx, tenantID, connectorID)
	if err != nil {
		return false, err
	}
	if c == nil {
		return false, nil
	}
	if c.ScheduleKind == model.ConnectorScheduleManual || !connectorSchedulingEnabled(c) {
		return false, nil
	}
	active, err := s.Store.CountActiveJobsWithPayload(ctx, model.JobKindConnectorSync, c.TenantID, excludeJobID, map[string]interface{}{"connector_id": c.ID})
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	next, err := s.Store.ConnectorScheduleRunAfter(ctx, c, time.Now().UTC())
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]string{
		"connector_id": c.ID, "trigger": model.ConnectorRunTriggerScheduled, "user_id": connectorActorSystem,
	})
	key := "connector_sync:" + c.ID + ":" + next.UTC().Format("20060102T150405Z")
	return s.Runner.Enqueue(ctx, model.JobKindConnectorSync, key, c.TenantID, string(payload), next, 0)
}

// BootstrapConnectorSchedules re-arms recurring syncs after a restart: the
// durable queue holds pending jobs, but a connector whose next run was never
// enqueued (or whose job was lost) would otherwise stay idle forever.
func (s *Service) BootstrapConnectorSchedules(ctx context.Context) {
	if s.Runner == nil {
		return
	}
	rows, err := s.Store.ListSchedulableConnectors(ctx, time.Now().UTC())
	if err != nil {
		slog.Warn("connector schedule bootstrap list failed", "error", err)
		return
	}
	for i := range rows {
		c := &rows[i]
		if _, serr := s.ScheduleConnectorNext(ctx, c.TenantID, c.ID, ""); serr != nil {
			slog.Warn("connector schedule bootstrap failed", "connector_id", c.ID, "error", serr)
		}
	}
}

// ScheduleConnectorRetention arms the platform-wide daily run-history purge.
func (s *Service) ScheduleConnectorRetention(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindConnectorRetention, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	now := time.Now().UTC()
	key := "connector_retention:" + now.Format("20060102")
	return s.Runner.Enqueue(ctx, model.JobKindConnectorRetention, key, SystemTenantID, "", now.Add(24*time.Hour), 0)
}

// connectorSkippedItem records why one provider object was skipped. Detail
// rows are bounded so a huge source cannot bloat run history.
type connectorSkippedItem struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
	Size   int64  `json:"size,omitempty"`
}

// connectorRunStats accumulates one sync's counters.
type connectorRunStats struct {
	Added   int
	Changed int
	Deleted int
	Skipped int
	Failed  int
	// Unchanged counts objects skipped by the cursor fast-path; they are
	// summarized rather than listed individually.
	Unchanged int
	// Failures carries bounded per-item error summaries (never credentials).
	Failures []string
	// SkippedItems carries bounded skip reasons (currently oversize objects).
	SkippedItems []connectorSkippedItem
}

const (
	connectorFailureDetailLimit = 20
	connectorSkippedDetailLimit = 50
	connectorSkippedKeyLimit    = 256
)

func (st *connectorRunStats) addFailure(msg string) {
	st.Failed++
	if len(st.Failures) < connectorFailureDetailLimit {
		st.Failures = append(st.Failures, msg)
	}
}

func (st *connectorRunStats) addSkip(key, reason string, size int64) {
	st.Skipped++
	if len(st.SkippedItems) >= connectorSkippedDetailLimit {
		return
	}
	if r := []rune(key); len(r) > connectorSkippedKeyLimit {
		key = string(r[:connectorSkippedKeyLimit]) + "…"
	}
	st.SkippedItems = append(st.SkippedItems, connectorSkippedItem{Key: key, Reason: reason, Size: size})
}

// connectorSchedulingEnabled is the single scheduling gate: operator intent
// (Enabled) and runtime health (Status) must both allow a run.
func connectorSchedulingEnabled(c *model.Connector) bool {
	return c != nil && c.Enabled && c.Status != model.ConnectorStatusDisabled
}

// connectorRuntime resolves provider, spec and decrypted credential.
func (s *Service) connectorRuntime(ctx context.Context, c *model.Connector) (connectorpkg.Provider, map[string]any, map[string]string, error) {
	p := s.connectorProviders().New(c.Provider)
	if p == nil {
		return nil, nil, nil, httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	spec, err := parseConnectorSpec(c.SpecJSON)
	if err != nil {
		return nil, nil, nil, httperr.BadRequest(40333, errConnectorSpecInvalid+": "+err.Error())
	}
	cred, err := s.connectorCredential(ctx, c)
	if err != nil {
		return nil, nil, nil, err
	}
	return p, spec, cred, nil
}

// syncConnectorOnce runs the scan -> diff -> apply -> cursor pipeline
// (doc/225 §8). It is context-bounded by the caller's hard deadline and
// persists the cursor after every page so an interrupted run keeps progress
// (F5); deletions are only tombstoned after a complete scan.
func (s *Service) syncConnectorOnce(ctx context.Context, c *model.Connector) (*connectorRunStats, error) {
	p, spec, cred, err := s.connectorRuntime(ctx, c)
	if err != nil {
		return nil, err
	}
	stats := &connectorRunStats{}
	cursor := parseConnectorCursor(c.CursorJSON)
	prevKeys := make([]string, 0, len(cursor))
	for k := range cursor {
		prevKeys = append(prevKeys, k)
	}
	seen := make(map[string]struct{}, len(cursor))
	token := ""
	lastCheck := time.Now()
	completed := false

	for {
		entries, next, lerr := p.List(ctx, spec, cred, token)
		if lerr != nil {
			return stats, fmt.Errorf("connector scan failed: %w", lerr)
		}
		if len(entries) == 0 {
			completed = true
			break
		}
		settings, _ := s.connectorSettings()
		maxObjectBytes := int64(settings.MaxObjectMB) << 20
		for _, entry := range entries {
			if cerr := ctx.Err(); cerr != nil {
				return stats, cerr
			}
			oldRevision, exists := cursor[entry.Key]
			seen[entry.Key] = struct{}{}
			if exists && oldRevision == entry.Revision {
				stats.Skipped++
				stats.Unchanged++
				continue
			}
			if entry.Size > maxObjectBytes {
				// doc/225 §6.2: oversize objects are skipped(large), not failed;
				// remember the revision so the next run does not re-download it.
				stats.addSkip(entry.Key, "large", entry.Size)
				cursor[entry.Key] = entry.Revision
				continue
			}
			content, derr := p.Download(ctx, spec, cred, entry.Key)
			if derr != nil {
				stats.addFailure(entry.Key + ": download: " + derr.Error())
				continue
			}
			_, uerr := s.UploadDocumentWithSourceKey(ctx, c.TenantID, c.TargetDatasetID, connectorUploader, path.Base(entry.Key), connectorSourceKey(c, entry.Key), content)
			if uerr != nil {
				stats.addFailure(entry.Key + ": upload: " + uerr.Error())
				continue
			}
			if exists {
				stats.Changed++
			} else {
				stats.Added++
			}
			cursor[entry.Key] = entry.Revision
		}
		// F5: checkpoint after each page; brand-new keys stay "added" for the
		// page that delivered them, and later pages see them as unchanged.
		if _, serr := s.Store.SaveConnectorCursor(ctx, c.TenantID, c.ID, mustCursorJSON(cursor)); serr != nil {
			return stats, serr
		}
		// doc/225 F8: re-check cancellation at least every batch and every 5s.
		if nowCheck := time.Now(); nowCheck.Sub(lastCheck) >= 5*time.Second {
			if cerr := ctx.Err(); cerr != nil {
				return stats, cerr
			}
			lastCheck = nowCheck
		}
		if next == "" {
			completed = true
			break
		}
		token = next
	}

	if !completed {
		return stats, ctx.Err()
	}

	// Deletions are authoritative only after a complete scan (F5): a cancelled
	// run must never tombstone the unscanned tail.
	for _, k := range prevKeys {
		if _, ok := seen[k]; ok {
			continue
		}
		if terr := s.TombstoneIncrementalSource(ctx, c.TenantID, c.TargetDatasetID, connectorSourceKey(c, k), "connector source deleted"); terr != nil {
			stats.addFailure(k + ": tombstone: " + terr.Error())
			continue
		}
		delete(cursor, k)
		stats.Deleted++
	}

	if _, serr := s.Store.SaveConnectorCursor(ctx, c.TenantID, c.ID, mustCursorJSON(cursor)); serr != nil {
		return stats, serr
	}
	return stats, nil
}

func mustCursorJSON(cursor map[string]string) string {
	b, err := json.Marshal(cursor)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func connectorSourceKey(c *model.Connector, providerKey string) string {
	// Namespace ledger source keys per connector so two connectors writing to
	// the same dataset never collide on the (tenant,dataset,source_key) triple.
	return "connector:" + c.ID + ":" + providerKey
}

// connectorCredential decrypts and parses a connector's sealed credential
// into the provider credential map (doc/225 §9 F1: fail closed on decrypt
// errors — never fall back to plaintext reads of the source).
func (s *Service) connectorCredential(ctx context.Context, c *model.Connector) (map[string]string, error) {
	_ = ctx
	if c.CredentialCrypted == "" {
		return map[string]string{}, nil
	}
	plain, err := crypto.Decrypt(s.EncryptKey, c.CredentialCrypted)
	if err != nil {
		slog.Error("connector credential decrypt failed", "connector_id", c.ID, "error", err)
		obs.Get().IncConnectorCredentialFailure()
		notify.Emit(ctx, notify.Event{
			ID: id.New(), Type: "connector.credential_failed", Title: "连接器凭据解密失败",
			Severity: "error", TenantID: c.TenantID, Resource: "connector", ResourceID: c.ID,
			ObjectType: "connector", Action: "connector.credential_failed",
			Detail: "credential decrypt failed", Fields: map[string]string{"provider": c.Provider},
		})
		return nil, httperr.New(500, 40340, errConnectorCredentialFailed)
	}
	return parseConnectorCred(plain)
}

func (s *Service) recordConnectorAudit(ctx context.Context, tenantID, action, objectID, detail string) {
	entry := &model.AuditLog{
		ID:         id.New(),
		TenantID:   tenantID,
		UserID:     "",
		Action:     action,
		Resource:   "connector",
		ResourceID: objectID,
		DetailJSON: truncateAuditDetail(detail),
		Scope:      "workspace",
		Result:     "success",
		At:         time.Now().UTC(),
	}
	if err := s.RecordAudit(ctx, entry); err != nil {
		slog.Warn("connector audit write failed", "action", action, "object_id", objectID, "error", err)
	}
}

func truncateAuditDetail(detail string) string {
	// Rune-safe: audit details may carry CJK content; a raw byte cut would
	// emit invalid UTF-8 that PostgreSQL rejects on write.
	return strutil.Truncate(detail, 1800)
}

// runConnectorSync executes one run row end-to-end: acquire the process-wide
// semaphore, enforce the per-run deadline, persist terminal fields, refresh
// connector status and emit metrics/notifications. It never leaks credential
// material into run details.
func (s *Service) runConnectorSync(ctx context.Context, c *model.Connector, trigger, triggeredBy string) (*model.ConnectorRun, error) {
	cfg, sem := s.connectorSettings()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	started := time.Now().UTC()
	triggeredBy = strings.TrimSpace(triggeredBy)
	if triggeredBy == "" {
		triggeredBy = connectorActorSystem
	}
	run := &model.ConnectorRun{
		ID: id.New(), ConnectorID: c.ID, TenantID: c.TenantID,
		Trigger: trigger, TriggeredBy: triggeredBy,
		Status: model.ConnectorRunStatusRunning, StartedAt: &started,
	}
	if err := s.Store.CreateConnectorRun(ctx, run); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.RunTimeoutSec)*time.Second)
	stats, serr := s.syncConnectorOnce(runCtx, c)
	cancel()

	finished := time.Now().UTC()
	runStatus := model.ConnectorRunStatusSucceeded
	connectorStatus := model.ConnectorStatusIdle
	switch {
	case errors.Is(serr, context.DeadlineExceeded):
		// doc/225 §6.2: the hard deadline ends the run as failed(timeout), not
		// as a user cancellation.
		runStatus = model.ConnectorRunStatusFailed
		connectorStatus = model.ConnectorStatusDegraded
	case errors.Is(serr, context.Canceled):
		runStatus = model.ConnectorRunStatusCancelled
		connectorStatus = model.ConnectorStatusDegraded
	case serr != nil || stats.Failed > 0:
		runStatus = model.ConnectorRunStatusFailed
		connectorStatus = model.ConnectorStatusDegraded
	}
	if !c.Enabled {
		connectorStatus = model.ConnectorStatusDisabled
	}
	if stats == nil {
		stats = &connectorRunStats{}
	}
	errSummary := ""
	switch {
	case errors.Is(serr, context.DeadlineExceeded):
		errSummary = "timeout: " + serr.Error()
	case serr != nil:
		errSummary = serr.Error()
	case len(stats.Failures) > 0:
		errSummary = strings.Join(stats.Failures, "; ")
	}
	if len(errSummary) > 500 {
		errSummary = strutil.Truncate(errSummary, 500)
	}
	detail := map[string]any{
		"failures":          stats.Failures,
		"skipped":           stats.SkippedItems,
		"skipped_unchanged": stats.Unchanged,
	}
	detailJSON, _ := json.Marshal(detail)

	outcome := repository.ConnectorRunOutcome{
		Status: runStatus, StartedAt: &started, FinishedAt: &finished,
		Added: stats.Added, Changed: stats.Changed, Deleted: stats.Deleted,
		Skipped: stats.Skipped, Failed: stats.Failed,
		DurationMS: finished.Sub(started).Milliseconds(), ErrorSummary: errSummary,
		DetailJSON: string(detailJSON),
	}
	// The parent context may already be canceled (user stopped the run). Terminal
	// bookkeeping must still land, otherwise the run stays "running" forever and
	// blocks the next manual trigger with 409/40336.
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistCancel()
	if _, rerr := s.Store.SetConnectorRunResult(persistCtx, run.ID, outcome); rerr != nil {
		slog.Warn("connector run result persist failed", "run_id", run.ID, "error", rerr)
	}
	// Status/last-run bookkeeping happens in the same durable step as the
	// cursor commit, so a failed run still flips the connector to degraded.
	if _, cerr := s.Store.SetConnectorRunState(persistCtx, c.TenantID, c.ID, &finished, runStatus, connectorStatus); cerr != nil {
		slog.Warn("connector status persist failed", "connector_id", c.ID, "error", cerr)
	}
	s.observeConnectorRun(c.Provider, trigger, runStatus, finished.Sub(started), stats)
	s.notifyConnectorRun(ctx, c, runStatus, errSummary, stats)
	return run, serr
}

func (s *Service) observeConnectorRun(provider, trigger, status string, dur time.Duration, stats *connectorRunStats) {
	o := obs.Get()
	o.RecordConnectorRun(provider, trigger, status, dur.Seconds())
	o.AddConnectorSyncItems("added", int64(stats.Added))
	o.AddConnectorSyncItems("changed", int64(stats.Changed))
	o.AddConnectorSyncItems("deleted", int64(stats.Deleted))
	o.AddConnectorSyncItems("skipped", int64(stats.Skipped))
	o.AddConnectorSyncItems("failed", int64(stats.Failed))
}

func (s *Service) notifyConnectorRun(ctx context.Context, c *model.Connector, runStatus, errSummary string, stats *connectorRunStats) {
	eventType := "connector.sync_succeeded"
	severity := "info"
	title := "连接器同步成功"
	if runStatus != model.ConnectorRunStatusSucceeded {
		eventType = "connector.sync_failed"
		severity = "error"
		title = "连接器同步失败"
	}
	notify.Emit(ctx, notify.Event{
		ID: id.New(), Type: eventType, Title: title, Severity: severity,
		TenantID: c.TenantID, Resource: "connector", ResourceID: c.ID,
		ObjectType: "connector", Action: eventType,
		Detail: errSummary,
		Fields: map[string]string{
			"provider": c.Provider,
			"status":   runStatus,
			"added":    fmt.Sprintf("%d", stats.Added),
			"changed":  fmt.Sprintf("%d", stats.Changed),
			"deleted":  fmt.Sprintf("%d", stats.Deleted),
			"failed":   fmt.Sprintf("%d", stats.Failed),
		},
	})
}

// connectorSyncWorker is the JobKindConnectorSync consumer.
type connectorSyncWorker struct {
	svc *Service
}

// Kind implements Worker.
func (w *connectorSyncWorker) Kind() string { return model.JobKindConnectorSync }

// Run implements Worker: one bounded sync then self-schedules the next run.
func (w *connectorSyncWorker) Run(ctx context.Context, job *model.Job) error {
	var p struct {
		ConnectorID string `json:"connector_id"`
		Trigger     string `json:"trigger"`
		UserID      string `json:"user_id"`
	}
	_ = json.Unmarshal([]byte(job.Payload), &p)
	if p.ConnectorID == "" {
		return fmt.Errorf("connector_sync payload missing connector_id")
	}
	c, err := w.svc.Store.GetConnector(ctx, job.TenantID, p.ConnectorID)
	if err != nil {
		return err
	}
	if !connectorSchedulingEnabled(c) {
		return nil
	}
	trigger := p.Trigger
	if trigger == "" {
		trigger = model.ConnectorRunTriggerScheduled
	}
	_, serr := w.svc.runConnectorSync(ctx, c, trigger, p.UserID)
	retryable := connectorSyncRetryable(serr)
	if retryable && !connectorFinalAttempt(job) {
		// doc/225 §6.1: let the durable Job framework retry with its
		// exponential backoff. Do not arm the next periodic run yet, otherwise
		// the retry and the next schedule could overlap for the same connector.
		return serr
	}
	// Success, a permanent error, or the last retry: always keep the recurring
	// schedule alive so a failed run does not silently stop future syncs.
	if _, rerr := w.svc.ScheduleConnectorNext(ctx, c.TenantID, c.ID, job.ID); rerr != nil {
		slog.Warn("connector reschedule failed", "connector_id", c.ID, "error", rerr)
	}
	if retryable {
		return serr
	}
	return nil
}

// connectorSyncRetryable distinguishes transient whole-run failures (scan,
// cursor persistence, connectivity) from deterministic configuration errors.
// Context cancellation/timeout and invalid spec/credential failures are not
// retried: the next scheduled run or an operator action is the right recovery.
func connectorSyncRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var he *httperr.Error
	if errors.As(err, &he) && (he.Code == 40333 || he.Code == 40340) {
		return false
	}
	return true
}

// connectorFinalAttempt reports whether the currently claimed attempt is the
// last one allowed by the Job framework.
func connectorFinalAttempt(job *model.Job) bool {
	if job == nil {
		return true
	}
	maxRetry := job.MaxRetry
	if maxRetry <= 0 {
		maxRetry = 3
	}
	return job.Attempts+1 >= maxRetry
}

// connectorRetentionWorker is the JobKindConnectorRetention consumer (doc/225
// §6.2 F4): it prunes expired run history only and re-arms the daily cycle.
type connectorRetentionWorker struct {
	svc *Service
}

// Kind implements Worker.
func (w *connectorRetentionWorker) Kind() string { return model.JobKindConnectorRetention }

// Run implements Worker.
func (w *connectorRetentionWorker) Run(ctx context.Context, job *model.Job) error {
	cfg, _ := w.svc.connectorSettings()
	cutoff := time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays)
	counts, err := w.svc.Store.DeleteExpiredConnectorRuns(ctx, cutoff)
	if err != nil {
		return err
	}
	var total int64
	for provider, rows := range counts {
		if rows <= 0 {
			continue
		}
		obs.Get().AddConnectorRunsPruned(provider, rows)
		total += rows
	}
	slog.Info("connector run retention purge", "deleted", total, "by_provider", counts, "cutoff", cutoff.Format(time.RFC3339))
	if _, rerr := w.svc.ScheduleConnectorRetention(ctx, job.ID); rerr != nil {
		return rerr
	}
	return nil
}

// SealConnectorApprovalPayload replaces a plaintext credential map with an
// AES-256-GCM sealed envelope before the payload is persisted in the approval
// table (doc/225 §9: credentials never land in plaintext).
func (s *Service) SealConnectorApprovalPayload(payload map[string]any) (map[string]any, error) {
	raw, ok := payload["credential"].(map[string]any)
	if !ok || len(raw) == 0 {
		return payload, nil
	}
	cred := map[string]string{}
	for k, v := range raw {
		if str, ok := v.(string); ok {
			cred[k] = str
		}
	}
	if len(cred) == 0 {
		return payload, nil
	}
	plain, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	sealed, err := crypto.Encrypt(s.EncryptKey, string(plain))
	if err != nil {
		return nil, httperr.Internal("failed to encrypt connector credential")
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	delete(out, "credential")
	out["credential_sealed"] = sealed
	return out, nil
}

func (s *Service) openConnectorApprovalCredential(payload map[string]any) (map[string]any, error) {
	sealed, ok := payload["credential_sealed"].(string)
	if !ok || sealed == "" {
		return payload, nil
	}
	plain, err := crypto.Decrypt(s.EncryptKey, sealed)
	if err != nil {
		return nil, httperr.Internal("failed to decrypt connector approval credential")
	}
	var cred map[string]string
	if err := json.Unmarshal([]byte(plain), &cred); err != nil {
		return nil, httperr.Internal("invalid connector approval credential")
	}
	out := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		out[k] = v
	}
	delete(out, "credential_sealed")
	anyCred := make(map[string]any, len(cred))
	for k, v := range cred {
		anyCred[k] = v
	}
	out["credential"] = anyCred
	return out, nil
}

// executeConnectorApproval performs the connector governance action decided by
// an approved approval row (doc/225 §7.1): approval authorizes the run without
// re-prompting.
func (s *Service) executeConnectorApproval(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	payload, err = s.openConnectorApprovalCredential(payload)
	if err != nil {
		return nil, err
	}
	switch approval.Action {
	case model.ApprovalActionCreate:
		in, ierr := connectorCreateInputFromPayload(payload)
		if ierr != nil {
			return nil, ierr
		}
		in.CreatedBy = approval.RequesterID
		c, cerr := s.CreateConnector(ctx, approval.TenantID, in)
		if cerr != nil {
			return nil, cerr
		}
		s.recordConnectorAudit(ctx, approval.TenantID, "connector.create.approved", c.ID, "via approval "+approval.ID)
		return map[string]any{"connector_id": c.ID}, nil
	case model.ApprovalActionUpdate:
		in, ierr := connectorUpdateInputFromPayload(payload)
		if ierr != nil {
			return nil, ierr
		}
		c, cerr := s.UpdateConnector(ctx, false, []string{approval.TenantID}, approval.ObjectID, in)
		if cerr != nil {
			return nil, cerr
		}
		s.recordConnectorAudit(ctx, approval.TenantID, "connector.update.approved", c.ID, "via approval "+approval.ID)
		return map[string]any{"connector_id": c.ID}, nil
	case model.ApprovalActionDelete:
		if derr := s.DeleteConnector(ctx, false, []string{approval.TenantID}, approval.ObjectID); derr != nil {
			return nil, derr
		}
		s.recordConnectorAudit(ctx, approval.TenantID, "connector.delete.approved", approval.ObjectID, "via approval "+approval.ID)
		return map[string]any{"connector_id": approval.ObjectID}, nil
	case model.ApprovalActionRotateCredential:
		in, ierr := connectorUpdateInputFromPayload(payload)
		if ierr != nil {
			return nil, ierr
		}
		c, cerr := s.UpdateConnector(ctx, false, []string{approval.TenantID}, approval.ObjectID, in)
		if cerr != nil {
			return nil, cerr
		}
		s.recordConnectorAudit(ctx, approval.TenantID, "connector.credential_rotate.approved", c.ID, "via approval "+approval.ID)
		return map[string]any{"connector_id": c.ID}, nil
	default:
		return nil, httperr.BadRequest(40060, "unsupported connector approval action")
	}
}

// validateConnectorApproval checks approval payload integrity before queueing.
func validateConnectorApproval(_ context.Context, svc *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	if approval.Action == model.ApprovalActionDelete {
		// Delete approvals carry no provider payload; the executor resolves the
		// object by approval.ObjectID.
		if strings.TrimSpace(approval.ObjectID) == "" {
			return httperr.BadRequest(40089, "connector id is required")
		}
		return nil
	}
	provider, _ := payload["provider"].(string)
	provider = strings.TrimSpace(provider)
	if approval.Action == model.ApprovalActionCreate {
		name, _ := payload["name"].(string)
		if strings.TrimSpace(name) == "" {
			return httperr.BadRequest(40089, "connector name is required")
		}
		if provider == "" {
			return httperr.BadRequest(40089, "connector provider is required")
		}
	}
	// Update / rotate approvals may be credential-only and omit the provider;
	// only reject an explicitly unsupported one.
	if provider != "" && svc.connectorProviders().New(provider) == nil {
		return httperr.BadRequest(40333, errConnectorProviderUnsup)
	}
	return nil
}

func connectorPayloadString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func connectorCreateInputFromPayload(payload map[string]any) (ConnectorCreateInput, error) {
	out := ConnectorCreateInput{
		Name:                connectorPayloadString(payload, "name"),
		Provider:            connectorPayloadString(payload, "provider"),
		AdapterKind:         connectorPayloadString(payload, "adapter_kind"),
		ExternalConnectorID: connectorPayloadString(payload, "external_connector_id"),
		TargetDatasetID:     connectorPayloadString(payload, "target_dataset_id"),
	}
	spec, _ := payload["spec"].(map[string]any)
	if spec != nil {
		out.SpecJSON = mustSpecJSON(spec)
	}
	cred, _ := payload["credential"].(map[string]any)
	if cred != nil {
		m := map[string]string{}
		for k, v := range cred {
			if s, ok := v.(string); ok {
				m[k] = s
			}
		}
		b, _ := json.Marshal(m)
		out.CredentialJSON = string(b)
	}
	if sch, ok := payload["schedule"].(string); ok {
		out.ScheduleKind = sch
	}
	if v, ok := payload["schedule_value"].(float64); ok {
		out.ScheduleValue = int(v)
	}
	if v, ok := payload["schedule_tz_offset_min"].(float64); ok {
		out.ScheduleTZOffsetMin = int(v)
	}
	if v, ok := payload["sensitive"].(bool); ok {
		out.Sensitive = v
	}
	if v, ok := payload["enabled"].(bool); ok {
		out.Enabled = &v
	}
	return out, nil
}

func connectorUpdateInputFromPayload(payload map[string]any) (ConnectorUpdateInput, error) {
	out := ConnectorUpdateInput{
		Name:                connectorPayloadString(payload, "name"),
		Provider:            connectorPayloadString(payload, "provider"),
		AdapterKind:         connectorPayloadString(payload, "adapter_kind"),
		ExternalConnectorID: connectorPayloadString(payload, "external_connector_id"),
		TargetDatasetID:     connectorPayloadString(payload, "target_dataset_id"),
	}
	spec, _ := payload["spec"].(map[string]any)
	if spec != nil {
		out.SpecJSON = mustSpecJSON(spec)
	}
	cred, _ := payload["credential"].(map[string]any)
	if cred != nil {
		m := map[string]string{}
		for k, v := range cred {
			if s, ok := v.(string); ok {
				m[k] = s
			}
		}
		b, _ := json.Marshal(m)
		out.CredentialJSON = string(b)
	}
	if sch, ok := payload["schedule"].(string); ok {
		out.ScheduleKind = sch
	}
	if v, ok := payload["schedule_value"].(float64); ok {
		out.ScheduleValue = int(v)
	}
	if v, ok := payload["schedule_tz_offset_min"].(float64); ok {
		out.ScheduleTZOffsetMin = int(v)
		out.ScheduleTZOffsetSet = true
	}
	if v, ok := payload["sensitive"].(bool); ok {
		out.Sensitive = &v
	}
	if v, ok := payload["enabled"].(bool); ok {
		out.Enabled = &v
	}
	return out, nil
}
