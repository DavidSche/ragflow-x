package service

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const maxAgentAttachmentBytes = 20 << 20

// UploadAgentFile uploads a temporary attachment to RAGFlow's Agent file
// context. RAGFlow parses the blob at completion time and exposes it to
// workflow components as sys.files.
func (s *Service) UploadAgentFile(ctx context.Context, tenantID, agentID, userID, filename string, content []byte) (*ragflow.UploadedFile, error) {
	agent, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, false)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, httperr.NotFound("agent not found")
	}

	filename = strings.TrimSpace(filename)
	if filename == "" {
		return nil, httperr.BadRequest(40071, "filename is required")
	}
	if len(content) == 0 {
		return nil, httperr.BadRequest(40072, "file is empty")
	}
	if len(content) > maxAgentAttachmentBytes {
		return nil, httperr.BadRequest(40073, "file too large (max 20MB)")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedChatAttachmentExts[ext] {
		return nil, httperr.BadRequest(40074, "unsupported file type: "+ext)
	}

	uploaded, err := s.RAGFlow.UploadAgentFile(ctx, agentID, filename, content)
	if err != nil {
		return nil, httperr.New(502, 50292, "ragflow upload agent file failed")
	}
	uploaded.UploadTicket = s.signAgentAttachment(agentAttachmentClaims{
		FileID:    uploaded.ID,
		Name:      uploaded.Name,
		Size:      uploaded.Size,
		Extension: uploaded.Extension,
		MimeType:  uploaded.MimeType,
		CreatedBy: uploaded.CreatedBy,
		AgentID:   agentID,
		TenantID:  tenantID,
		UserID:    userID,
		ExpiresAt: time.Now().Add(agentAttachmentTicketTTL).Unix(),
	})
	return uploaded, nil
}
