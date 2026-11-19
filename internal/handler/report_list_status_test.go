package handler

// 「列表响应必须带上当前版本的交付状态」。
//
// 服务层已经钉过（`TestReportListCarriesTheCurrentRevisionDeliveryState`），这里
// 钉的是**线上真正发出去的那份 JSON**：字段名、嵌入方式、null 形状。服务层返回
// 结构体、handler 用 `response.OKPage` 包一层，`ReportListRow` 要是把
// `model.Report` 嵌错了位置，`items[]` 里就没有 `revision_status`，前端只能退回
// 那个把「存续」写成「进行中」的生命周期标记——一个只在序列化层出现的缺陷。

import (
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportListResponseCarriesRevisionStatus(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "list-status", model.RoleOperator)
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, "policy-list-status")

	rec, envelope := fx.do(t, http.MethodPost, "/api/v1/reports", "key-list-status-0001",
		reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d (%s)", rec.Code, rec.Body.String())
	}
	created, _ := envelope["data"].(map[string]interface{})
	reportRow, _ := created["report"].(map[string]interface{})
	reportID, _ := reportRow["id"].(string)

	rec, envelope = fx.do(t, http.MethodGet, "/api/v1/reports?page=1&page_size=20", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d (%s)", rec.Code, rec.Body.String())
	}
	page, _ := envelope["data"].(map[string]interface{})
	if page == nil {
		t.Fatalf("list must answer a page envelope, got %v", envelope)
	}
	items, _ := page["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected one row, got %d", len(items))
	}
	entry, _ := items[0].(map[string]interface{})
	if entry["id"] != reportID {
		t.Fatalf("unexpected row: %v", entry)
	}

	// The row still speaks about the report (title/question/expiry) — the delivery
	// fields are added next to them, not instead of them.
	if entry["title"] == nil || entry["expires_at"] == nil || entry["status"] != model.ReportStatusActive {
		t.Fatalf("report fields missing from the list row: %v", entry)
	}
	// A freshly created report has revision 1 running (or awaiting the outline).
	// Either way the field is present and non-empty: an absent field is the defect.
	status, _ := entry["revision_status"].(string)
	if status == "" {
		t.Fatalf("list row must carry revision_status, got %v", entry)
	}
	if status != model.ReportRevisionGenerating && status != model.ReportRevisionAwaitingOutline &&
		status != model.ReportRevisionDraft {
		t.Fatalf("unexpected revision_status %q for a fresh report", status)
	}
	if revision, _ := entry["revision"].(float64); revision != 1 {
		t.Fatalf("list row must carry revision=1, got %v", entry["revision"])
	}
	if _, ok := entry["completeness"]; !ok {
		t.Fatalf("list row must carry completeness (empty until publish): %v", entry)
	}
	if _, ok := entry["published_at"]; !ok {
		t.Fatalf("list row must carry published_at (null until publish): %v", entry)
	}
}
