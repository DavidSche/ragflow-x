package main

import (
	"os"
	"path/filepath"
	"testing"
)

// This file holds the regression acceptance that doc/301 §9.1 rules 3/5 demand, and
// that the round-4 review asked for by name:
//
//	同一 chunk 在 A 节被引用、在 B 节作为候选时，A 节引用必须保留，B 节候选也必须保留，
//	并正确标记 cited_elsewhere=true。
//
// It is worth more than "候选数量 > 0", because the count alone cannot tell a
// candidate set that survived from a candidate set that was rebuilt by deleting the
// citation that competed with it. Both halves are asserted below.

// TestMergeRevisionKeepsBothRoles is the offline half: one content entity, two role
// associations, one revision, two sections.
func TestMergeRevisionKeepsBothRoles(t *testing.T) {
	items := []Item{
		{ClaimID: "c-a", ClaimText: "断言 A", SectionOrd: 0, SectionPrompt: "节 0 提示"},
		{ClaimID: "c-b", ClaimText: "断言 B", SectionOrd: 1, SectionPrompt: "节 1 提示"},
	}
	cites := map[string][]Evidence{
		// A 节引用了 chunk X。
		"c-a": {{SnapshotID: "s-x", ChunkID: "X", ContentHash: "hx", Content: "X 的原文"}},
		"c-b": {{SnapshotID: "s-y", ChunkID: "Y", ContentHash: "hy", Content: "Y 的原文"}},
	}
	cands := map[int][]Candidate{
		// B 节把同一个 chunk X 冻结成了候选；Z 是没人引用过的干净候选。
		1: {
			{ChunkID: "X", ContentHash: "hx", PoolRank: 1, Content: "X 的原文"},
			{ChunkID: "Z", ContentHash: "hz", PoolRank: 2, Content: "Z 的原文"},
			// B 节自己引用了 Y，它必须被排除（§9.1 规则 4）。
			{ChunkID: "Y", ContentHash: "hy", PoolRank: 3, Content: "Y 的原文"},
		},
	}
	src := Source{RevisionID: "rev-1", ReportID: "rep-1", RevisionStatus: "published"}

	out := mergeRevision("rev-1", items, cites, cands, src, "2026-11-21")

	byClaim := map[string]Item{}
	for _, it := range out {
		byClaim[it.ClaimID] = it
	}

	a, ok := byClaim["c-a"]
	if !ok {
		t.Fatal("断言 A 被丢掉了：引用侧不得因为别处有同名候选而消失")
	}
	// ① 引用侧保留：A 节的 citation 仍然是 A 节 item 的证据。
	if len(a.Evidence) != 1 || a.Evidence[0].ChunkID != "X" {
		t.Fatalf("A 节的引用必须原样保留，实际: %+v", a.Evidence)
	}
	if len(a.CandidatePool) != 0 {
		t.Fatalf("A 节没有冻结候选，不该凭空多出 %+v", a.CandidatePool)
	}
	if a.GoldScanScope != "cited_only" {
		t.Fatalf("A 节 scan_scope 应为 cited_only，实际 %q", a.GoldScanScope)
	}

	b, ok := byClaim["c-b"]
	if !ok {
		t.Fatal("断言 B 被丢掉了")
	}
	// ② 候选侧保留 + 标记：X 在 B 节活下来，并且说自己被别处引用过。
	candByChunk := map[string]Candidate{}
	for _, c := range b.CandidatePool {
		candByChunk[c.ChunkID] = c
	}
	x, ok := candByChunk["X"]
	if !ok {
		t.Fatalf("B 节的候选 X 被删掉了：这正是 D-08 之前的错误，实际: %+v", b.CandidatePool)
	}
	if !x.CitedElsewhere {
		t.Fatalf("候选 X 必须标记 cited_elsewhere=true，实际: %+v", x)
	}
	if z, ok := candByChunk["Z"]; !ok || z.CitedElsewhere {
		t.Fatalf("未被引用的候选 Z 应存活且不标记，实际: %+v", b.CandidatePool)
	}
	// ③ 本节引用仍然排除（导出阶段不得绕过冻结阶段的跳过集）。
	if _, ok := candByChunk["Y"]; ok {
		t.Fatalf("B 节自己引用的 Y 必须被排除，实际: %+v", b.CandidatePool)
	}
	// ④ 披露宽度 = 实际带出的行数。
	if b.GoldScanScope != "cited+candidate:2" {
		t.Fatalf("B 节 scan_scope 应为 cited+candidate:2，实际 %q", b.GoldScanScope)
	}

	// ⑤ 一个 chunk，两个角色，且两边都在。
	if a.Evidence[0].ChunkID == "X" && x.ChunkID == "X" {
		if a.Evidence[0].ContentHash != x.ContentHash {
			t.Fatal("同一 chunk 的两个角色必须指向同一内容实体")
		}
	}
}

// TestPoolExportKeepsCitedElsewhereCitations is the same property against the real
// database: if an export says a candidate was cited elsewhere, then some item of that
// same revision must still carry that citation as evidence. A flag without a citation
// behind it would mean the export deleted the citation side and labelled the remains.
func TestPoolExportKeepsCitedElsewhereCitations(t *testing.T) {
	dsn := liveDSN(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "pool.jsonl")

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	code := run([]string{"pool", "--dsn", dsn, "--out", out, "--max-revisions", "40"}, devnull, devnull)
	if code != exitOK && code != exitIncomplete {
		t.Fatalf("pool exited %d; must be 0 or 3", code)
	}
	items, err := readItems(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}

	// revision → (chunk key → 被哪条 item 引用)
	cited := map[string]map[string][]string{}
	for _, it := range items {
		key := it.Source.RevisionID
		if cited[key] == nil {
			cited[key] = map[string][]string{}
		}
		for _, ev := range it.Evidence {
			k := ev.ChunkID + ":" + ev.ContentHash
			cited[key][k] = append(cited[key][k], it.ClaimID)
		}
	}

	flagged := 0
	for _, it := range items {
		for _, c := range it.CandidatePool {
			if !c.CitedElsewhere {
				continue
			}
			flagged++
			k := c.ChunkID + ":" + c.ContentHash
			owners := cited[it.Source.RevisionID][k]
			if len(owners) == 0 {
				t.Fatalf("item %q 说候选 %s 被别处引用，但同一修订里没有任何 item 引用它"+
					"（导出删掉了引用侧，cited_elsewhere 就成了无根据的标记）", it.ItemID, k)
			}
			for _, claim := range owners {
				if claim == it.ClaimID {
					t.Fatalf("item %q 的候选 %s 标记 cited_elsewhere，但它自己就引用了这个 chunk"+
						"（§9.1 规则 4：本节引用必须排除）", it.ItemID, k)
				}
			}
		}
	}
	if flagged == 0 {
		t.Skip("本次导出没有任何 cited_elsewhere 行；这条验收没有分母，不做结论")
	}
	t.Logf("cited_elsewhere 验收：%d 行标记，全部能在同一修订的引用侧找到出处", flagged)
}
