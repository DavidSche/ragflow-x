package service

import "testing"

// The dataset upload guard is the only place that decides which files a
// knowledge base accepts. It drifted from `chat_upload.go` (which has always
// taken spreadsheets) and from the engine (which parses them), so the
// spreadsheet formats that our own table-scoring evaluation depends on were
// refused with a bare 400 (doc/286 §2).
func TestValidateDocumentUploadAcceptsSpreadsheets(t *testing.T) {
	body := []byte("col1,col2\n1,2\n")
	for _, name := range []string{
		"notes.txt", "guide.md", "manual.pdf", "report.docx",
		"ledger.csv", "legacy.xls", "隐患台账.xlsx",
	} {
		if err := validateDocumentUpload(name, body); err != nil {
			t.Fatalf("%s must be accepted, got %v", name, err)
		}
	}
}

func TestValidateDocumentUploadStillRefusesUnknownFormats(t *testing.T) {
	err := validateDocumentUpload("payload.exe", []byte("MZ"))
	if err == nil {
		t.Fatal("an unknown extension must be refused")
	}
	if msg := err.Error(); !contains(msg, "csv") || !contains(msg, "xlsx") {
		t.Fatalf("refusal must advertise the accepted formats, got %q", msg)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
