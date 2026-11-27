package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// This file owns the four content writers of the generate path (doc/288 §7.3,
// §7.4, §7.9; T3/T4). They live apart from report.go because the aggregate file
// is already 1.5k lines and none of these methods belongs to the creation or the
// terminal path: creation freezes intent, these freeze *work*, finalize freezes
// the verdict. Two rules shape all four:
//
//  1. Every write is a CAS on the slice or the section attempt that owns it. A
//     duplicated slice therefore cannot overwrite what a live slice is writing —
//     the loser gets `applied=false`, which is not an error (the winner already
//     recorded the truth).
//  2. The frozen execution snapshot is written exactly once, by the outline step,
//     before the first model call (§10.11). Later attempts reuse it and only the
//     hash guard decides, never the caller's intent.

// ReportOutlineStart parks a running job in `awaiting_outline`.
//
// The job row, the revision and the section skeleton move in one transaction
// because each of them alone is a lie: a job "awaiting confirmation" without an
// outline has nothing to confirm, one without sections would arm a slice with an
// empty skeleton, and one without the frozen snapshot would let the next attempt
// generate under different conditions than the reviewer approved.
type ReportOutlineStart struct {
	TenantID      string
	JobID         string
	RevisionID    string
	RuntimeJobID  string
	AttemptSeq    int
	SliceSeq      int
	Now           time.Time
	OutlineJSON   string
	SnapshotJSON  string
	SnapshotHash  string
	EngineVersion string
	Sections      []model.ReportSection
	Event         *model.ReportJobEvent
}

// BeginReportOutline is a CAS on the slice's own pointer: only the slice that
// owns `(attempt_seq, slice_seq, current_runtime_job_id)` may park the job.
//
// `status = running` is part of the guard on purpose: a job the user cancelled
// mid-outline must converge through finalize, not silently start waiting for a
// confirmation nobody will ever give.
func (s *store) BeginReportOutline(ctx context.Context, start ReportOutlineStart) (bool, error) {
	if start.OutlineJSON == "" || len(start.Sections) == 0 {
		return false, errors.New("report outline requires an outline and at least one section")
	}
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportJob{}).
			Where(`tenant_id = ? AND id = ? AND attempt_seq = ? AND slice_seq = ? AND
				current_runtime_job_id = ? AND finalized_at IS NULL AND status = ?`,
				start.TenantID, start.JobID, start.AttemptSeq, start.SliceSeq,
				start.RuntimeJobID, model.ReportJobRunning).
			Updates(map[string]interface{}{
				"status":                 model.ReportJobAwaitingOutline,
				"stage":                  model.ReportStageOutlining,
				"current_runtime_job_id": "",
				"progress_done":          0,
				"last_checkpoint_at":     start.Now,
				"updated_at":             start.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Model(&model.ReportRevision{}).
			Where("tenant_id = ? AND id = ?", start.TenantID, start.RevisionID).
			Updates(map[string]interface{}{
				"status":       model.ReportRevisionAwaitingOutline,
				"outline_json": start.OutlineJSON,
			}).Error; err != nil {
			return err
		}
		if err := applyReportExecutionSnapshotInTx(
			tx, start.TenantID, start.RevisionID, start.SnapshotJSON, start.SnapshotHash, start.EngineVersion,
		); err != nil {
			return err
		}
		if err := tx.Create(&start.Sections).Error; err != nil {
			return err
		}
		if start.Event != nil {
			if _, err := appendReportEventInTx(tx, start.Event); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// applyReportExecutionSnapshotInTx freezes the generation conditions once.
//
// The guard is on the hash column, not on the caller: a retried outline step (or
// a P1 regeneration) must reuse the conditions the first attempt recorded, which
// is what makes "same conditions" verifiable instead of aspirational (§7.3
// explicitly does not promise a byte-identical replay).
func applyReportExecutionSnapshotInTx(
	tx *gorm.DB, tenantID, revisionID, snapshotJSON, hash, engineVersion string,
) error {
	if snapshotJSON == "" || hash == "" {
		return nil
	}
	updates := map[string]interface{}{
		"execution_snapshot_json": snapshotJSON,
		"execution_snapshot_hash": hash,
	}
	if engineVersion != "" {
		updates["engine_version"] = engineVersion
	}
	return tx.Model(&model.ReportRevision{}).
		Where(`tenant_id = ? AND id = ? AND
			(execution_snapshot_hash IS NULL OR execution_snapshot_hash = '')`,
			tenantID, revisionID).
		Updates(updates).Error
}

// ReportOutlineConfirm arms the writing attempt after the reviewer confirmed.
type ReportOutlineConfirm struct {
	TenantID   string
	JobID      string
	RevisionID string
	Now        time.Time
	// Titles carries the reviewer's edits, keyed by `section_key`. P0-A accepts
	// titles only: letting the confirmation rewrite a prompt would change what
	// the frozen execution snapshot describes while the snapshot stays frozen.
	Titles []ReportSectionTitle
	Slice  ReportSliceStart
	Event  *model.ReportJobEvent
}

// ReportSectionTitle is one reviewer-edited section title.
type ReportSectionTitle struct {
	SectionKey string
	Title      string
}

// ConfirmReportOutline moves `awaiting_outline` to `running` on a *new* business
// attempt (`attempt_seq + 1`), which is why the first confirmation of a job can
// never collide with the slice keys of the attempt that produced the outline.
func (s *store) ConfirmReportOutline(ctx context.Context, confirm ReportOutlineConfirm) (bool, error) {
	slice := confirm.Slice
	if slice.AttemptSeq < 2 || slice.SliceSeq != 1 {
		return false, errors.New("report outline confirmation requires a fresh attempt starting at slice 1")
	}
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportJob{}).
			Where("tenant_id = ? AND id = ? AND finalized_at IS NULL AND status = ?",
				confirm.TenantID, confirm.JobID, model.ReportJobAwaitingOutline).
			Updates(map[string]interface{}{
				"status":                 model.ReportJobRunning,
				"attempt_seq":            slice.AttemptSeq,
				"slice_seq":              slice.SliceSeq,
				"current_runtime_job_id": slice.RuntimeJobID,
				"stage":                  slice.Stage,
				"progress_done":          0,
				"started_at":             gorm.Expr("COALESCE(started_at, ?)", confirm.Now),
				"last_checkpoint_at":     confirm.Now,
				"updated_at":             confirm.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Model(&model.ReportRevision{}).
			Where("tenant_id = ? AND id = ?", confirm.TenantID, confirm.RevisionID).
			Update("status", model.ReportRevisionGenerating).Error; err != nil {
			return err
		}
		for _, title := range confirm.Titles {
			// Only a section that has not started may be retitled: `writing` and
			// later rows carry the title they were written under, and rewriting it
			// would make the stored content disagree with its own heading.
			if err := tx.Model(&model.ReportSection{}).
				Where(`tenant_id = ? AND revision_id = ? AND section_key = ? AND status = ?`,
					confirm.TenantID, confirm.RevisionID, title.SectionKey, model.ReportSectionPending).
				Update("title", title.Title).Error; err != nil {
				return err
			}
		}
		if err := insertReportSliceInTx(tx, slice); err != nil {
			return err
		}
		if confirm.Event == nil {
			return nil
		}
		if _, err := appendReportEventInTx(tx, confirm.Event); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ReportSectionStart marks one section as `writing` under a new attempt.
type ReportSectionStart struct {
	TenantID        string
	JobID           string
	RevisionID      string
	SectionID       string
	PreviousAttempt int
	Now             time.Time
	PromptHash      string
	Stage           string
	Event           *model.ReportJobEvent
}

// BeginReportSection is the "a call is about to happen" write.
//
// `status IN (pending, failed, writing)` is deliberate. `pending`/`failed` are the
// ordinary retry sources; `writing` is the corpse of a slice that died mid-call,
// and refusing to restart it would strand the section forever. The attempt CAS is
// what makes accepting `writing` safe: the live writer has already bumped
// `attempt`, so a duplicate start fails the guard instead of stealing the row.
func (s *store) BeginReportSection(ctx context.Context, start ReportSectionStart) (bool, error) {
	next := start.PreviousAttempt + 1
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportSection{}).
			Where(`tenant_id = ? AND revision_id = ? AND id = ? AND attempt = ? AND
				status IN ?`, start.TenantID, start.RevisionID, start.SectionID,
				start.PreviousAttempt,
				[]string{model.ReportSectionPending, model.ReportSectionFailed, model.ReportSectionWriting}).
			Updates(map[string]interface{}{
				"status":      model.ReportSectionWriting,
				"attempt":     next,
				"started_at":  start.Now,
				"finished_at": nil,
				"prompt_hash": start.PromptHash,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := touchReportJobStageInTx(tx, start.TenantID, start.JobID, start.Stage, start.Now); err != nil {
			return err
		}
		if start.Event != nil {
			if _, err := appendReportEventInTx(tx, start.Event); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ReportSectionReuse carries one base section into a new revision (doc/288 §7.4 /
// R14).
//
// The caller prepares the *new* rows — new ids, new `revision_id`/`section_id`,
// same `evidence_snapshot_id` — because id minting is the service's job
// (`reportRowID`); this type exists so the copy is one transaction instead of four
// writes that can half-fail. The snapshot ids are deliberately *reused*: a snapshot
// is an immutable observation of a source (§7.5.1), so two revisions citing the
// same chunk should share it rather than freeze a second copy of the same bytes.
type ReportSectionReuse struct {
	TenantID   string
	JobID      string
	RevisionID string
	// SectionID is the new revision's `pending` row to fill in.
	SectionID string
	// BaseSection is the live section row being inherited. It is read inside the
	// transaction, not passed in: the caller's copy could be stale, and
	// `based_on_section_version` must name the version that actually exists.
	BaseSectionID string
	Now           time.Time
	Stage         string
	// Citations and Claims are the copies to insert, already re-keyed onto the new
	// section. An empty list is legitimate (a section written with no evidence).
	Citations []model.ReportCitation
	Claims    []model.ReportClaim
	Event     *model.ReportJobEvent
}

// ReuseReportSection inherits one section body and its evidence links.
//
// `applied=false` is a *normal* answer with two causes, both handled by the caller
// writing the section normally instead:
//
//   - the target row is no longer `pending` (a retried outline step, or a
//     concurrent slice already wrote it) — the CAS declined;
//   - a cited evidence snapshot is gone. This is the case that matters: inheriting
//     the body and the citation *rows* while the snapshot they point at has expired
//     produces a searchable-looking report whose appendix cannot be exported
//     (§7.6 / A43 answer 44116). The copy is refused instead, so a reuse request can
//     degrade to a fresh write but never to an unexportable report.
func (s *store) ReuseReportSection(ctx context.Context, plan ReportSectionReuse) (bool, error) {
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var base model.ReportSection
		if err := tx.Where("tenant_id = ? AND id = ?", plan.TenantID, plan.BaseSectionID).
			First(&base).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if base.Status != model.ReportSectionSucceeded {
			// Only a written body is inheritable. A failed or skipped base row would
			// copy a hole and label it as content.
			return nil
		}
		if err := assertReportSnapshotsLiveInTx(tx, plan.TenantID, plan.Citations); err != nil {
			if errors.Is(err, errReportReuseEvidenceMissing) {
				// Evidence gone: decline the inheritance, let the caller write the
				// section normally. Not an error — the request was fine.
				return nil
			}
			return err
		}
		result := tx.Model(&model.ReportSection{}).
			Where(`tenant_id = ? AND revision_id = ? AND id = ? AND status = ?`,
				plan.TenantID, plan.RevisionID, plan.SectionID, model.ReportSectionPending).
			Updates(map[string]interface{}{
				"status":                   model.ReportSectionSucceeded,
				"based_on_section_version": base.SectionVersion,
				"rewrite_reason":           model.ReportRewriteReasonReuse,
				"content_md":               base.ContentMD,
				"content_hash":             base.ContentHash,
				"prompt_hash":              base.PromptHash,
				"tokens_in":                base.TokensIn,
				"tokens_out":               base.TokensOut,
				"citation_count":           len(plan.Citations),
				"claim_count":              len(plan.Claims),
				"evidence_status":          base.EvidenceStatus,
				"quality_status":           base.QualityStatus,
				"finished_at":              plan.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if len(plan.Citations) > 0 {
			if err := tx.Create(&plan.Citations).Error; err != nil {
				return err
			}
		}
		if len(plan.Claims) > 0 {
			if err := tx.Create(&plan.Claims).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.ReportJob{}).
			Where("tenant_id = ? AND id = ?", plan.TenantID, plan.JobID).
			Updates(map[string]interface{}{
				"progress_done":      gorm.Expr("CASE WHEN progress_done + 1 > progress_total THEN progress_total ELSE progress_done + 1 END"),
				"stage":              plan.Stage,
				"last_checkpoint_at": plan.Now,
				"updated_at":         plan.Now,
			}).Error; err != nil {
			return err
		}
		if plan.Event != nil {
			if _, err := appendReportEventInTx(tx, plan.Event); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// assertReportSnapshotsLiveInTx refuses to copy citations whose snapshot is gone.
//
// One query for the whole set: the reuse path must not become a per-citation read,
// and "all present" is the only answer that permits the copy — a partial answer
// would silently drop a citation and leave a `[3]` marker pointing at nothing.
func assertReportSnapshotsLiveInTx(
	tx *gorm.DB, tenantID string, citations []model.ReportCitation,
) error {
	ids := make([]string, 0, len(citations))
	seen := make(map[string]struct{}, len(citations))
	for _, citation := range citations {
		id := citation.EvidenceSnapshotID
		if id == "" {
			return errReportReuseEvidenceMissing
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	// A tombstone is not inheritable evidence (doc/319 R-01): the content it froze
	// is gone, so a section that reuses it would cite a snapshot nothing can be
	// verified against. Counting it as live here would let the reuse path mint a
	// section whose assurance run can only ever say `evidence_expired`.
	var live int64
	if err := tx.Model(&model.ReportEvidenceSnapshot{}).
		Where("tenant_id = ? AND id IN ? AND purged_at IS NULL", tenantID, ids).
		Count(&live).Error; err != nil {
		return err
	}
	if live != int64(len(ids)) {
		return errReportReuseEvidenceMissing
	}
	return nil
}

// errReportReuseEvidenceMissing is the internal signal for "this section cannot be
// inherited". It is mapped to `applied=false` by the caller, never returned to a
// client: the reuse request was valid, the knowledge base simply moved on.
var errReportReuseEvidenceMissing = errors.New("report section reuse: evidence snapshot is no longer available")

// ReportSectionCommit writes the section, its citations and its evidence.
type ReportSectionCommit struct {
	TenantID string
	JobID    string
	Now      time.Time
	// Section is the row to write. Only the content-bearing columns are copied
	// into the UPDATE (see the allowlist below), so a caller cannot smuggle a
	// status or a key change through this path.
	Section   model.ReportSection
	Citations []model.ReportCitation
	// Claims are the R11 rows extracted from the body (§9.2). They are written in
	// the same transaction as the content because a claim row that outlived its
	// body, or a body whose claims were dropped, is exactly the unattributable
	// report the claim table exists to prevent.
	Claims   []model.ReportClaim
	Evidence []model.ReportEvidenceSnapshot
	Stage    string
	Event    *model.ReportJobEvent
}

// CommitReportSection is the section's happy ending: content, hashes, tokens,
// citations bound to evidence snapshot ids, and the progress counter — one
// transaction, so "succeeded" never exists without the content that justifies it.
func (s *store) CommitReportSection(ctx context.Context, commit ReportSectionCommit) (bool, error) {
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportSection{}).
			Where(`tenant_id = ? AND revision_id = ? AND id = ? AND attempt = ? AND status = ?`,
				commit.TenantID, commit.Section.RevisionID, commit.Section.ID,
				commit.Section.Attempt, model.ReportSectionWriting).
			Updates(map[string]interface{}{
				"status":          model.ReportSectionSucceeded,
				"content_md":      commit.Section.ContentMD,
				"content_hash":    commit.Section.ContentHash,
				"tokens_in":       commit.Section.TokensIn,
				"tokens_out":      commit.Section.TokensOut,
				"citation_count":  commit.Section.CitationCount,
				"claim_count":     commit.Section.ClaimCount,
				"evidence_status": commit.Section.EvidenceStatus,
				"quality_status":  commit.Section.QualityStatus,
				// R18: what the deep-research planner asked for and what it added.
				// Written in the same statement as the body so a section can never
				// claim a plan it was not written under.
				"research_plan_json": commit.Section.ResearchPlanJSON,
				// doc/301 §5.8 item 1: the writing-time pool, written with the body it
				// produced. `nil` writes SQL NULL on purpose — a section written before
				// this column existed must read back as "not recorded", never as "the
				// pool was empty".
				"pool_hit_chunks":      commit.Section.PoolHitChunks,
				"pool_insufficient":    commit.Section.PoolInsufficient,
				"pool_candidate_count": commit.Section.PoolCandidateCount,
				"finished_at":          commit.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		evidenceIDs, err := bindReportEvidenceInTx(tx, commit.TenantID, commit.Evidence)
		if err != nil {
			return err
		}
		for index := range commit.Citations {
			citation := &commit.Citations[index]
			snapshotID, ok := evidenceIDs[reportEvidenceKey(citation.SourceChunkID, citation.SourceContentHash)]
			if !ok {
				// A citation whose evidence row is not in this transaction is a
				// citation to nothing; §7.5 forbids "引用没有证据" outright, so the
				// section fails to commit rather than publishing an unbacked claim.
				return gorm.ErrRecordNotFound
			}
			citation.EvidenceSnapshotID = snapshotID
		}
		if len(commit.Citations) > 0 {
			if err := tx.Create(&commit.Citations).Error; err != nil {
				return err
			}
		}
		if len(commit.Claims) > 0 {
			if err := tx.Create(&commit.Claims).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.ReportJob{}).
			Where("tenant_id = ? AND id = ?", commit.TenantID, commit.JobID).
			Updates(map[string]interface{}{
				// `MIN(a, b)` is the *scalar* two-argument form, which SQLite has and
				// PostgreSQL does not: there the aggregate of that name takes one
				// argument, so `MIN(bigint, bigint)` fails with SQLSTATE 42883 and the
				// section commit transaction aborts. A CASE is the same arithmetic in
				// both dialects. (Live defect, doc/44 §6.)
				"progress_done":      gorm.Expr("CASE WHEN progress_done + 1 > progress_total THEN progress_total ELSE progress_done + 1 END"),
				"stage":              commit.Stage,
				"last_checkpoint_at": commit.Now,
				"updated_at":         commit.Now,
			}).Error; err != nil {
			return err
		}
		if commit.Event != nil {
			if _, err := appendReportEventInTx(tx, commit.Event); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// bindReportEvidenceInTx dedupes the evidence snapshots of one section and
// returns their ids keyed by `(chunk_id, source_content_hash)`.
//
// The dedupe is a database guarantee (`uidx_rgx_report_evidence_dedup`), so two
// sections citing the same chunk store one snapshot and cite it twice. That is
// the point: evidence is an immutable observation of a source, not a per-section
// attachment, and a citation is the join.
func bindReportEvidenceInTx(
	tx *gorm.DB, tenantID string, evidence []model.ReportEvidenceSnapshot,
) (map[string]string, error) {
	if len(evidence) == 0 {
		return map[string]string{}, nil
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "chunk_id"}, {Name: "source_content_hash"}},
		// The dedupe identity is (chunk, source content hash), so a conflicting
		// insert can only ever carry *identical* content; what it may change is the
		// observation clock — and, when retention already tombstoned the row, the
		// tombstone itself. Reviving is the honest answer (doc/319 R-01): the
		// citation being written points at content that exists again, and leaving
		// `purged_at` set would make that brand-new citation read as
		// `evidence_expired`. `DoNothing` was safe only while a purge deleted the
		// row, because there was nothing left to collide with.
		DoUpdates: clause.AssignmentColumns([]string{
			"content", "content_hash", "content_size", "truncated", "truncation_strategy",
			"source_parsed_at", "expires_at", "purged_at", "purged_reason",
		}),
	}).Create(&evidence).Error; err != nil {
		return nil, err
	}
	chunkIDs := make([]string, 0, len(evidence))
	seen := make(map[string]bool, len(evidence))
	for _, row := range evidence {
		if seen[row.ChunkID] {
			continue
		}
		seen[row.ChunkID] = true
		chunkIDs = append(chunkIDs, row.ChunkID)
	}
	var rows []model.ReportEvidenceSnapshot
	if err := tx.Select("id, chunk_id, source_content_hash").
		Where("tenant_id = ? AND chunk_id IN ?", tenantID, chunkIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(rows))
	for _, row := range rows {
		ids[reportEvidenceKey(row.ChunkID, row.SourceContentHash)] = row.ID
	}
	return ids, nil
}

// reportEvidenceKey is the dedupe identity of one evidence snapshot.
func reportEvidenceKey(chunkID, sourceContentHash string) string {
	return chunkID + "\x00" + sourceContentHash
}

// ReportSectionClose writes the section's honest ending: failed or skipped.
type ReportSectionClose struct {
	TenantID       string
	JobID          string
	RevisionID     string
	SectionID      string
	Attempt        int
	Status         string
	QualityStatus  string
	EvidenceStatus string
	Now            time.Time
	Stage          string
	Event          *model.ReportJobEvent
	// Audit is the `report.section.failed` ledger row (doc/288 §11.3). It rides
	// this transaction so "the section ended failed" and "someone may read why"
	// cannot disagree: an audit written afterwards is skipped whenever
	// this close loses its attempt CAS or the process dies between the two
	// writes, and the report would then show a failed section nobody can trace.
	Audit *model.AuditLog
}

// CloseReportSection fails or skips a section that is currently `writing`.
//
// The CAS on `(attempt, status='writing')` means a close can only ever end the
// attempt that is actually open, so a late failure from a dead slice cannot
// overwrite a section another slice has already committed.
func (s *store) CloseReportSection(ctx context.Context, closeReport ReportSectionClose) (bool, error) {
	if closeReport.Status != model.ReportSectionFailed && closeReport.Status != model.ReportSectionSkipped {
		return false, errors.New("report section close requires a failed or skipped status")
	}
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// `pending` is allowed next to `writing` because a failure can happen before
		// the section ever starts (a retrieval error, a beam of missing evidence
		// after the reservation). Refusing it would leave the row `pending` forever
		// and make the next slice pick the same failing section again.
		result := tx.Model(&model.ReportSection{}).
			Where(`tenant_id = ? AND revision_id = ? AND id = ? AND attempt = ? AND status IN ?`,
				closeReport.TenantID, closeReport.RevisionID, closeReport.SectionID,
				closeReport.Attempt, []string{model.ReportSectionPending, model.ReportSectionWriting}).
			Updates(map[string]interface{}{
				"status":          closeReport.Status,
				"quality_status":  closeReport.QualityStatus,
				"evidence_status": closeReport.EvidenceStatus,
				// A `failed` section's reason is `failed`; a direct close is never a
				// skip (SkipReportSections owns that word and its own reason).
				"gap_reason":  model.ReportGapFailed,
				"finished_at": closeReport.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := touchReportJobStageInTx(tx, closeReport.TenantID, closeReport.JobID, closeReport.Stage, closeReport.Now); err != nil {
			return err
		}
		if closeReport.Event != nil {
			if _, err := appendReportEventInTx(tx, closeReport.Event); err != nil {
				return err
			}
		}
		if closeReport.Audit != nil {
			if err := appendAuditInTx(tx, closeReport.Audit); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ReportSectionSkip closes the sections a run will never write.
//
// The guard is `status IN (pending, failed)` and not `writing`: a section that is
// being written right now has a live owner, and stealing it here would leave that
// owner's commit unable to land (its CAS would fail) with no one to explain why.
type ReportSectionSkip struct {
	TenantID       string
	JobID          string
	RevisionID     string
	SectionIDs     []string
	QualityStatus  string
	EvidenceStatus string
	// GapReason is the runner's own wording (`skipped` or `budget_exhausted`). It is
	// stored on the row so finalize does not have to guess it from the quality flag.
	GapReason string
	Now       time.Time
	Stage     string
	Events    []*model.ReportJobEvent
}

// SkipReportSections writes the skip in one transaction with its events, so a
// reader never sees "skipped" without the `section.failed` frame that explains it.
func (s *store) SkipReportSections(ctx context.Context, skip ReportSectionSkip) (int64, error) {
	if len(skip.SectionIDs) == 0 {
		return 0, nil
	}
	applied := int64(0)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportSection{}).
			Where("tenant_id = ? AND revision_id = ? AND id IN ? AND status IN ?",
				skip.TenantID, skip.RevisionID, skip.SectionIDs,
				[]string{model.ReportSectionPending, model.ReportSectionFailed}).
			Updates(map[string]interface{}{
				"status":          model.ReportSectionSkipped,
				"quality_status":  skip.QualityStatus,
				"evidence_status": skip.EvidenceStatus,
				"gap_reason":      skip.GapReason,
				"finished_at":     skip.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		applied = result.RowsAffected
		if applied == 0 {
			return nil
		}
		if err := touchReportJobStageInTx(tx, skip.TenantID, skip.JobID, skip.Stage, skip.Now); err != nil {
			return err
		}
		for _, event := range skip.Events {
			if event == nil {
				continue
			}
			if _, err := appendReportEventInTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}

// touchReportJobStageInTx bumps the job's stage clock.
//
// `last_checkpoint_at` moves with it because a section starting or failing is
// exactly the kind of progress the stale-active sweep must not mistake for a
// dead job. An empty stage means "the stage did not change".
func touchReportJobStageInTx(tx *gorm.DB, tenantID, jobID, stage string, now time.Time) error {
	updates := map[string]interface{}{
		"last_checkpoint_at": now,
		"updated_at":         now,
	}
	if stage != "" {
		updates["stage"] = stage
	}
	return tx.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", tenantID, jobID).
		Updates(updates).Error
}
