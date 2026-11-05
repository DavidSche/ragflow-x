package model

import "time"

// CitationReference preserves tenant/project-scoped citation evidence for an
// answer. Revision and the chunk snapshot remain readable after the document is
// replaced, while replaced chunks are not offered to new conversations.
type CitationReference struct {
	ID         string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ChatID     string `gorm:"column:chat_id;size:64;index" json:"chat_id"`
	SessionID  string `gorm:"column:session_id;size:64;index" json:"session_id"`
	RequestID  string `gorm:"column:request_id;size:128;index" json:"request_id"`
	DatasetID  string `gorm:"column:dataset_id;size:64;not null;index" json:"dataset_id"`
	DocumentID string `gorm:"column:document_id;size:64;not null;index" json:"document_id"`
	ChunkID    string `gorm:"column:chunk_id;size:64;not null;index" json:"chunk_id"`
	Revision   int64  `gorm:"column:revision;not null;default:0" json:"revision"`
	Content    string `gorm:"column:content;type:text" json:"content"`
	Keywords   string `gorm:"column:keywords;type:text" json:"keywords"`
	// Index is the zero-based position of this citation within the turn's
	// reference list, so the UI can map answer-side [n] markers to the drawer
	// without relying on opaque chunk ids (P2 citation experience).
	Index int `gorm:"column:ref_index;not null;default:0" json:"index,omitempty"`
	// Positions preserves the engine-provided in-document locator keys
	// (positions/bbox/page/offset/paragraph/line_*...) verbatim as JSON so the
	// forward UI can highlight the cited span inside the source document.
	Positions   string    `gorm:"column:positions;type:text" json:"positions,omitempty"`
	RetrievedAt time.Time `gorm:"column:retrieved_at;not null;index" json:"retrieved_at"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}

func (CitationReference) TableName() string { return "rgx_citation_reference" }
