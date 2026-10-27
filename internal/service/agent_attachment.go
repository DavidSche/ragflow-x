package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

const agentAttachmentTicketTTL = 2 * time.Hour

type agentAttachmentClaims struct {
	FileID    string `json:"f"`
	Name      string `json:"n"`
	Size      int64  `json:"s"`
	Extension string `json:"e,omitempty"`
	MimeType  string `json:"m,omitempty"`
	CreatedBy string `json:"c"`
	AgentID   string `json:"a"`
	TenantID  string `json:"t"`
	UserID    string `json:"u"`
	ExpiresAt int64  `json:"x"`
}

func (s *Service) signAgentAttachment(claims agentAttachmentClaims) string {
	payload, err := json.Marshal(claims)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, s.EncryptKey)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) resolveAgentAttachmentTickets(tenantID, agentID, userID string, files []map[string]interface{}) ([]map[string]interface{}, error) {
	resolved := make([]map[string]interface{}, 0, len(files))
	for index, file := range files {
		ticket, _ := file["upload_ticket"].(string)
		if ticket == "" {
			return nil, httperr.BadRequest(40075, fmt.Sprintf("files[%d] upload_ticket is required", index))
		}
		claims, err := s.verifyAgentAttachmentTicket(ticket, tenantID, agentID, userID)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, map[string]interface{}{
			"id": claims.FileID, "created_by": claims.CreatedBy, "name": claims.Name,
			"size": claims.Size, "extension": claims.Extension, "mime_type": claims.MimeType,
		})
	}
	return resolved, nil
}

func (s *Service) verifyAgentAttachmentTicket(ticket, tenantID, agentID, userID string) (*agentAttachmentClaims, error) {
	claims, err := s.parseAgentAttachmentTicket(ticket)
	if err != nil {
		return nil, httperr.BadRequest(40076, "invalid attachment reference")
	}
	if claims.TenantID != tenantID || claims.AgentID != agentID || claims.UserID != userID || claims.ExpiresAt <= time.Now().Unix() {
		return nil, httperr.Forbidden("attachment reference is not usable for this request")
	}
	return claims, nil
}

func (s *Service) parseAgentAttachmentTicket(ticket string) (*agentAttachmentClaims, error) {
	parts := strings.SplitN(ticket, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("malformed attachment ticket")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.EncryptKey)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("invalid attachment ticket signature")
	}
	var claims agentAttachmentClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if claims.FileID == "" || claims.CreatedBy == "" || claims.TenantID == "" || claims.AgentID == "" {
		return nil, errors.New("incomplete attachment ticket")
	}
	return &claims, nil
}
