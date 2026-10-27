package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type failingChatUploadClient struct {
	ragflow.Mock
}

func (c *failingChatUploadClient) UploadChatFile(context.Context, string, []byte) (*ragflow.UploadedFile, error) {
	return nil, errors.New("upstream unavailable")
}

func TestUploadChatFileValidation(t *testing.T) {
	svc := &Service{RAGFlow: ragflow.NewMock()}
	ctx := context.Background()

	cases := []struct {
		name     string
		filename string
		content  []byte
		code     int
	}{
		{name: "missing filename", filename: " ", content: []byte("data"), code: 40061},
		{name: "empty content", filename: "notes.txt", content: nil, code: 40062},
		{name: "oversized content", filename: "notes.txt", content: bytes.Repeat([]byte("a"), 20<<20+1), code: 40063},
		{name: "unsupported type", filename: "notes.exe", content: []byte("data"), code: 40064},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UploadChatFile(ctx, tc.filename, tc.content)
			httpErr, ok := err.(*httperr.Error)
			if !ok || httpErr.Code != tc.code {
				t.Fatalf("expected http error %d, got %#v", tc.code, err)
			}
		})
	}
}

func TestUploadChatFileExtractsPlainTextAndDocx(t *testing.T) {
	svc := &Service{RAGFlow: ragflow.NewMock()}
	ctx := context.Background()

	txt, err := svc.UploadChatFile(ctx, "notes.txt", []byte("plain body"))
	if err != nil {
		t.Fatal(err)
	}
	if txt.Content != "plain body" {
		t.Fatalf("plain text content was not preserved: %q", txt.Content)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	file, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("<w:p><w:t>Hello</w:t></w:p><w:p><w:t>World</w:t></w:p>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	docx, err := svc.UploadChatFile(ctx, "report.docx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if docx.Content != "Hello\nWorld" {
		t.Fatalf("unexpected docx excerpt: %q", docx.Content)
	}
}

func TestUploadChatFileWrapsProviderFailure(t *testing.T) {
	svc := &Service{RAGFlow: &failingChatUploadClient{}}
	_, err := svc.UploadChatFile(context.Background(), "notes.txt", []byte("body"))
	httpErr, ok := err.(*httperr.Error)
	if !ok || httpErr.Status != 502 || httpErr.Code != 50232 {
		t.Fatalf("expected upstream 502 error, got %#v", err)
	}
}
