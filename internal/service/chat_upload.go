package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// allowedChatAttachmentExts gates which uploads are allowed as chat attachments.
var allowedChatAttachmentExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".txt": true, ".md": true,
	".csv": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".bmp": true,
}

const maxChatAttachmentBytes = 20 << 20 // 20MB

// UploadChatFile uploads a temporary chat attachment to the engine and returns
// its file metadata so the frontend can attach it to a chat message.
func (s *Service) UploadChatFile(ctx context.Context, filename string, content []byte) (*ragflow.UploadedFile, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return nil, httperr.BadRequest(40061, "filename is required")
	}
	if len(content) == 0 {
		return nil, httperr.BadRequest(40062, "file is empty")
	}
	if len(content) > maxChatAttachmentBytes {
		return nil, httperr.BadRequest(40063, "file too large (max 20MB)")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedChatAttachmentExts[ext] {
		return nil, httperr.BadRequest(40064, "unsupported file type: "+ext)
	}
	uploaded, err := s.RAGFlow.UploadChatFile(ctx, filename, content)
	if err != nil {
		return nil, httperr.New(502, 50232, "ragflow upload chat file failed")
	}
	uploaded.Content = extractAttachmentText(filename, content)
	return uploaded, nil
}

// extractAttachmentText returns a readable text excerpt for a chat attachment.
// Plain-text formats are returned verbatim; docx are unpacked from their zip.
func extractAttachmentText(filename string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md", ".csv", ".json", ".log", ".html":
		return string(content)
	case ".docx":
		return extractDocxText(content)
	default:
		return ""
	}
}

func extractDocxText(content []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return ""
	}
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "word/document.xml") {
			rc, err := f.Open()
			if err != nil {
				return ""
			}
			b, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr != nil {
				return ""
			}
			return stripDocxXml(string(b))
		}
	}
	return ""
}

var docxXmlTag = regexp.MustCompile(`<[^>]*>`)

func stripDocxXml(s string) string {
	// Keep paragraph boundaries so the excerpt reads like the source document.
	s = strings.ReplaceAll(s, "</w:p>", "\n")
	s = docxXmlTag.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	return strings.TrimSpace(s)
}
