package db

import "testing"

func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}

func TestPermissionCatalog(t *testing.T) {
	cat := PermissionCatalog()
	if len(cat) == 0 {
		t.Fatal("permission catalog must not be empty")
	}
	byRes := map[string][]string{}
	for _, e := range cat {
		byRes[e.Resource] = e.Actions
	}
	for _, res := range []string{"user", "role", "tenant", "dataset", "audit", "system", "assistant", "agent", "ragflow-sync"} {
		if _, ok := byRes[res]; !ok {
			t.Fatalf("catalog missing resource %q", res)
		}
	}
	if !contains(byRes["system"], "manage") {
		t.Fatalf("system manage missing in catalog: %v", byRes["system"])
	}
	if !contains(byRes["dataset"], "read") {
		t.Fatalf("dataset read missing in catalog: %v", byRes["dataset"])
	}
	if !contains(byRes["assistant"], "read") {
		t.Fatalf("assistant read missing in catalog: %v", byRes["assistant"])
	}
	if !contains(byRes["agent"], "session:create") {
		t.Fatalf("agent session:create missing in catalog: %v", byRes["agent"])
	}
	if !contains(byRes["enterprise-connection"], "test") {
		t.Fatalf("enterprise-connection test missing in catalog: %v", byRes["enterprise-connection"])
	}
}
