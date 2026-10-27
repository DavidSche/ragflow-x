package ragflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestHTTPClient_ListCompilationTemplateGroupsContract(t *testing.T) {
	group := CompilationTemplateGroup{
		ID: "group-1", Name: "Policy Set", Description: "Rules", Scope: "file",
		CreatedAt: "2026-10-04 12:00:00", UpdatedAt: "2026-10-04 13:00:00",
		Templates: []CompilationTemplateGroupTemplate{{ID: "tree-1", Name: "Tree", Kind: "tree"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v1/compilation-template-groups" {
			t.Fatalf("unexpected list request: %s %s", r.Method, r.URL.EscapedPath())
		}
		query := r.URL.Query()
		if query.Get("keywords") != "Policy" || query.Get("scope") != "file" ||
			query.Get("page") != "2" || query.Get("page_size") != "20" ||
			query.Get("orderby") != "name" || query.Get("desc") != "false" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		writeEnvelope(t, w, map[string]interface{}{"groups": []CompilationTemplateGroup{group}, "total": 1})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	groups, total, err := client.ListCompilationTemplateGroups(context.Background(), CompilationTemplateGroupFilter{
		Keywords: "Policy", Scope: "file", Page: 2, PageSize: 20, Orderby: "name", Desc: false,
	})
	if err != nil || total != 1 || len(groups) != 1 || groups[0].ID != group.ID ||
		groups[0].Scope != "file" || len(groups[0].Templates) != 1 {
		t.Fatalf("groups=%+v total=%d err=%v", groups, total, err)
	}
}

func TestHTTPClient_CompilationTemplateGroupCRUDContract(t *testing.T) {
	group := CompilationTemplateGroup{
		ID: "group/1", Name: "Policy Set", Description: "Rules", Scope: "dataset",
		Templates: []CompilationTemplateGroupTemplate{{Name: "Wiki", Kind: "wiki"}},
	}
	request := CompilationTemplateGroupRequest{
		Name: group.Name, Description: group.Description,
		Templates: group.Templates,
	}
	var savedBody CompilationTemplateGroupRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/compilation-template-groups":
			if err := json.NewDecoder(r.Body).Decode(&savedBody); err != nil {
				t.Fatal(err)
			}
			writeEnvelope(t, w, group)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v1/compilation-template-groups/group%2F1":
			writeEnvelope(t, w, group)
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/v1/compilation-template-groups/group%2F1":
			writeEnvelope(t, w, group)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/v1/compilation-template-groups/group%2F1":
			writeEnvelope(t, w, true)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	ctx := context.Background()

	created, err := client.SaveCompilationTemplateGroup(ctx, request)
	if err != nil || created.ID != group.ID || len(created.Templates) != 1 ||
		savedBody.Name != request.Name || len(savedBody.Templates) != 1 {
		t.Fatalf("create=%+v saved=%+v err=%v", created, savedBody, err)
	}
	got, err := client.GetCompilationTemplateGroup(ctx, group.ID)
	if err != nil || got.ID != group.ID {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	updated, err := client.UpdateCompilationTemplateGroup(ctx, group.ID, request)
	if err != nil || updated.ID != group.ID {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	deleted, err := client.DeleteCompilationTemplateGroup(ctx, group.ID)
	if err != nil || !deleted {
		t.Fatalf("deleted=%t err=%v", deleted, err)
	}
}

func TestCompilationTemplateGroupValidation(t *testing.T) {
	client := NewHTTPClient("http://127.0.0.1:1", "key", time.Second, 1)
	ctx := context.Background()
	if _, _, err := client.ListCompilationTemplateGroups(ctx, CompilationTemplateGroupFilter{Page: 0, PageSize: 200}); err == nil {
		t.Fatal("invalid page bounds must fail")
	}
	if _, err := client.GetCompilationTemplateGroup(ctx, " "); err == nil {
		t.Fatal("empty group id must fail")
	}
	if _, err := client.SaveCompilationTemplateGroup(ctx, CompilationTemplateGroupRequest{}); err == nil {
		t.Fatal("invalid create payload must fail")
	}
	if err := validateCompilationTemplateGroupRequest(CompilationTemplateGroupRequest{Name: "Partial"}, false); err != nil {
		t.Fatalf("partial update without templates must be allowed: %v", err)
	}
	if _, err := client.UpdateCompilationTemplateGroup(ctx, "group-1", CompilationTemplateGroupRequest{Templates: []CompilationTemplateGroupTemplate{}}); err == nil {
		t.Fatal("invalid templates must fail")
	}
	if _, err := client.UpdateCompilationTemplateGroup(ctx, " ", CompilationTemplateGroupRequest{Name: "New"}); err == nil {
		t.Fatal("empty update id must fail")
	}
	if _, err := client.DeleteCompilationTemplateGroup(ctx, ""); err == nil {
		t.Fatal("empty delete id must fail")
	}
}

func TestMock_CompilationTemplateGroupCRUD(t *testing.T) {
	mock := NewMock()
	ctx := context.Background()
	request := CompilationTemplateGroupRequest{
		Name: "Policy Set", Description: "Rules",
		Templates: []CompilationTemplateGroupTemplate{{Name: "Wiki", Kind: "wiki", Config: map[string]interface{}{"topic": "policy"}}},
	}
	created, err := mock.SaveCompilationTemplateGroup(ctx, request)
	if err != nil || created.ID == "" || created.Scope != "dataset" || len(created.Templates) != 1 {
		t.Fatalf("mock create=%+v err=%v", created, err)
	}
	if _, _, err := mock.ListCompilationTemplateGroups(ctx, CompilationTemplateGroupFilter{Keywords: "Policy", Scope: "dataset"}); err != nil {
		t.Fatalf("mock list: %v", err)
	}
	if captured := mock.LastCompilationTemplateGroupListFilter(); captured == nil || captured.Keywords != "Policy" || captured.Scope != "dataset" {
		t.Fatalf("captured filter=%+v", captured)
	}
	got, err := mock.GetCompilationTemplateGroup(ctx, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("mock get=%+v err=%v", got, err)
	}
	request.Name = "Updated Policy Set"
	updated, err := mock.UpdateCompilationTemplateGroup(ctx, created.ID, request)
	if err != nil || updated.Name != request.Name {
		t.Fatalf("mock update=%+v err=%v", updated, err)
	}
	deleted, err := mock.DeleteCompilationTemplateGroup(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("mock delete=%t err=%v", deleted, err)
	}
	if missing, err := mock.GetCompilationTemplateGroup(ctx, created.ID); err != nil || missing != nil {
		t.Fatalf("mock get after delete=%+v err=%v", missing, err)
	}
}

func TestMock_CompilationTemplateGroupSeededList(t *testing.T) {
	groups := []CompilationTemplateGroup{{
		ID: "group-1", Name: "Seeded", Scope: "file",
		Templates: []CompilationTemplateGroupTemplate{{Name: "Tree", Kind: "tree"}},
	}}
	mock := NewMock()
	mock.SetCompilationTemplateGroups(groups)
	got, total, err := mock.ListCompilationTemplateGroups(context.Background(), CompilationTemplateGroupFilter{})
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != groups[0].ID {
		t.Fatalf("seeded groups=%+v total=%d err=%v", got, total, err)
	}
	if !reflect.DeepEqual(got[0].Templates, groups[0].Templates) {
		t.Fatalf("templates mismatch: %+v", got[0].Templates)
	}
}
