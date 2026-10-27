package connector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildPlan(t *testing.T) {
	prev := map[string]string{
		"a.txt":    "r1",
		"b.txt":    "r1",
		"gone.txt": "r9",
	}
	cur := []Entry{
		{Key: "a.txt", Revision: "r1"}, // unchanged
		{Key: "b.txt", Revision: "r2"}, // changed
		{Key: "c.txt", Revision: "r3"}, // added
	}
	added, changed, deleted := BuildPlan(prev, cur)
	if len(added) != 1 || added[0] != "c.txt" {
		t.Fatalf("added = %v, want [c.txt]", added)
	}
	if len(changed) != 1 || changed[0] != "b.txt" {
		t.Fatalf("changed = %v, want [b.txt]", changed)
	}
	if len(deleted) != 1 || deleted[0] != "gone.txt" {
		t.Fatalf("deleted = %v, want [gone.txt]", deleted)
	}
}

func TestRegistryBuiltins(t *testing.T) {
	r := NewRegistry()
	types := r.Types()
	if len(types) != 3 {
		t.Fatalf("registry types = %v, want 3 builtins", types)
	}
	for _, ty := range []string{"local_folder", "s3", "webdav"} {
		p := r.New(ty)
		if p == nil {
			t.Fatalf("registry missing %s", ty)
		}
		if p.Type() != ty {
			t.Fatalf("type mismatch: %s", p.Type())
		}
		meta, ok := r.MetaFor(ty)
		if !ok {
			t.Fatalf("registry missing meta for %s", ty)
		}
		if meta.Tier != TierNative || meta.Closure != ClosureFull {
			t.Fatalf("%s must stay a Tier A full-closure native source, got %s/%s", ty, meta.Tier, meta.Closure)
		}
		if meta.TargetVersion == "" {
			t.Fatalf("%s missing target version", ty)
		}
	}
}

func TestLocalFolderPaging(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.txt", "a.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden", "skip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &LocalFolder{}
	spec := map[string]any{"root": dir}
	if err := p.ValidateSpec(spec); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := p.Test(context.Background(), spec, nil); err != nil {
		t.Fatalf("test: %v", err)
	}

	var keys []string
	token := ""
	for {
		entries, next, err := p.List(context.Background(), spec, nil, token)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) == 0 {
			break
		}
		for _, e := range entries {
			keys = append(keys, e.Key)
			if e.Key == "skip.txt" {
				t.Fatalf("hidden file leaked into scan")
			}
			if e.Revision == "" {
				t.Fatalf("entry %s missing revision", e.Key)
			}
		}
		token = next
		if token == "" {
			break
		}
	}
	want := []string{"a.txt", "b.txt", "c.txt"}
	if len(keys) != len(want) {
		t.Fatalf("collected keys = %v, want %v", keys, want)
	}
}

func TestBuildPlanEmptyPage(t *testing.T) {
	prev := map[string]string{"gone.txt": "r1"}
	added, changed, deleted := BuildPlan(prev, nil)
	if len(added) != 0 || len(changed) != 0 || len(deleted) != 1 {
		t.Fatalf("empty page: added=%v changed=%v deleted=%v", added, changed, deleted)
	}
}

func TestS3CredentialAliases(t *testing.T) {
	for _, cred := range []map[string]string{
		{"access_key_id": "AK", "access_secret": "SK"},
		{"access_key": "AK", "secret_key": "SK"},
	} {
		ak, sk, err := s3Cred(cred)
		if err != nil || ak != "AK" || sk != "SK" {
			t.Fatalf("s3Cred(%v) = %q,%q,%v", cred, ak, sk, err)
		}
	}
	if _, _, err := s3Cred(map[string]string{"access_key": "AK"}); err == nil {
		t.Fatal("missing secret should fail")
	}
}
