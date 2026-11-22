package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The frozen pre-registration is a hash of two inputs that are *not in the repository*
// (doc/301 §9.1's worksheet and pool live under `tmp/gold/`, and `.gitignore:82` keeps them
// out). So on a clean clone `prereg --check` can only say "the inputs are absent, exit 3" —
// which leaves the first question unanswered: *is this even the file that was frozen?*
//
// Answering that needs no corpus: a digest of the frozen file itself (its sha256, its
// counts, and the recorded input hashes) is small, contains no text, and can be committed.
// Then a clean clone proves "byte-identical to what was frozen" and says exactly one thing
// is still unverifiable — the inputs' own hashes — instead of two (304 §7 item 18).
//
// What this deliberately does *not* do: it does not make the freeze tamper-proof. Someone
// who edits the frozen file and the digest together still passes. That is the documented
// threat model (operator error, not forgery — same sentence as 304 §7 item 19), and a
// signature is the follow-up, not a silent claim here.
const preregDigestSchema = "rgx.goldtool.prereg.digest/1"

// preregDigestInput is one frozen input, recorded *without its path*: the digest travels in
// the repository, and a path from someone's working directory ("E:/study-place/…") is both
// useless on another machine and needlessly specific. The basename is enough to tell an
// operator which file `--check` is waiting for.
type preregDigestInput struct {
	Role   string `json:"role"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Items  int    `json:"items"`
}

type preregDigestFile struct {
	Schema       string              `json:"schema"`
	PreregFile   string              `json:"prereg_file"`
	PreregSHA256 string              `json:"prereg_sha256"`
	Bytes        int                 `json:"bytes"`
	Experiment   string              `json:"experiment"`
	FrozenAt     string              `json:"frozen_at"`
	Author       string              `json:"author"`
	Inputs       []preregDigestInput `json:"inputs"`
	// Why is copied into the artifact so a reader who finds only the digest learns what it
	// proves and what it does not, without a link to a document they may not have.
	Why string `json:"why"`
}

const preregDigestWhy = "其输入（样本/池）是否仍然存在、是否被替换 —— 那些文件不在仓库里，" +
	"那部分仍以 exit 3 报告（doc/304 §7 item 18）"

// buildPreregDigest hashes the frozen file and records what it pins. It refuses to digest a
// file that fails its own checks: a digest of a broken freeze would make the breakage look
// like the thing that was agreed on.
func buildPreregDigest(p preregFile, path string) (preregDigestFile, error) {
	sum, size, err := fileSum(path)
	if err != nil {
		return preregDigestFile{}, err
	}
	d := preregDigestFile{
		Schema: preregDigestSchema, PreregFile: filepath.Base(path), PreregSHA256: sum, Bytes: size,
		Experiment: p.Experiment, FrozenAt: p.FrozenAt, Author: p.Author, Why: preregDigestWhy,
	}
	for _, in := range p.Inputs {
		d.Inputs = append(d.Inputs, preregDigestInput{Role: in.Role,
			Name: filepath.Base(in.Path), SHA256: in.SHA256, Items: in.Items})
	}
	return d, nil
}

func writePreregDigest(path string, d preregDigestFile) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	// O_EXCL, for the same reason the freeze itself uses it: a digest that can be rewritten
	// in place records the current file, not the frozen one.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readPreregDigest(path string) (preregDigestFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return preregDigestFile{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var d preregDigestFile
	if err := dec.Decode(&d); err != nil {
		return preregDigestFile{}, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return preregDigestFile{}, fmt.Errorf("%s: trailing content after the JSON object", path)
	}
	return d, nil
}

// checkPreregDigest compares a digest with the file it claims to pin. One question, one
// sentence: is this the frozen file? Identity is the hash, never the name — a copy that was
// renamed is still the same file, and a file that was edited keeps its name.
func checkPreregDigest(d preregDigestFile, path string) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	if d.Schema != preregDigestSchema {
		add("digest schema is %q, want %q", d.Schema, preregDigestSchema)
	}
	if len(d.PreregSHA256) != 64 || !isHex(d.PreregSHA256) {
		add("digest has no 64-hex prereg_sha256: it pins nothing")
		return problems
	}
	got, _, err := fileSum(path)
	if err != nil {
		add("%s cannot be hashed (%v): the digest cannot be compared with it", path, err)
		return problems
	}
	if got != d.PreregSHA256 {
		add("%s hashes to %s…, but the digest pins %s…: this is not the frozen file, so its thresholds "+
			"and sample plan describe a different pre-registration", path, got[:12], d.PreregSHA256[:12])
	}
	return problems
}

// checkPreregDigestAgrees cross-checks the digest's *copied* input records against the ones
// the frozen file itself carries. Checking only the file's own hash was not enough: the digest
// repeats each input's sha256 and row count, and a hand-edited copy of those numbers used to be
// accepted silently ("--check --digest-file passes" while the two records disagreed). Two
// records of the same fact that can disagree without a sentence are worse than one record: the
// reader cannot tell which one the freeze bound.
func checkPreregDigestAgrees(d preregDigestFile, p preregFile) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	if len(d.Inputs) != len(p.Inputs) {
		add("the digest records %d input(s), the frozen file records %d: one of the two was edited",
			len(d.Inputs), len(p.Inputs))
		return problems
	}
	for i, in := range p.Inputs {
		got := d.Inputs[i]
		want := filepath.Base(in.Path)
		if got.Role != in.Role || got.Name != want || got.SHA256 != in.SHA256 || got.Items != in.Items {
			add("the digest's input %d (%s %s, %d items, sha256 %s…) disagrees with the frozen file's "+
				"(%s %s, %d items, sha256 %s…): the digest is a copy, and the copy was edited",
				i+1, got.Role, got.Name, got.Items, firstN(got.SHA256, 12),
				in.Role, want, in.Items, firstN(in.SHA256, 12))
		}
	}
	return problems
}

// firstN shortens a hash for a sentence. Hashing is not the place to be clever: an empty
// string must stay readable rather than panic on a slice past the end.
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// summarisePreregDigest prints the artifact the digest is. Both sentences the operational
// reading needs are printed: what it proves, and what it still cannot.
func summarisePreregDigest(stdout *os.File, path string, d preregDigestFile) {
	fmt.Fprintf(stdout, "goldtool prereg: wrote the digest of %s → %s\n  sha256 %s  %d bytes  experiment %s  "+
		"frozen %s  author %s\n", d.PreregFile, path, d.PreregSHA256, d.Bytes, d.Experiment, d.FrozenAt, d.Author)
	for _, in := range d.Inputs {
		fmt.Fprintf(stdout, "  input %-6s %s (%d items) sha256 %s\n", in.Role, in.Name, in.Items, in.SHA256)
	}
	fmt.Fprintf(stdout, "  proves: the frozen file is byte-identical to the one that was frozen\n")
	fmt.Fprintf(stdout, "  不证明：%s\n", strings.TrimSpace(preregDigestWhy))
}

// runPreregDigest is `prereg --digest`: it pins the frozen file and writes nothing else.
// It refuses to digest a file that fails its own checks — a digest of a broken freeze would
// launder the breakage into "the thing that was agreed on" — but it does *not* need the
// corpus: the digest records the input hashes the freeze already carries.
func runPreregDigest(preregPath, out, unused string, stdout, stderr *os.File) int {
	if strings.TrimSpace(preregPath) == "" || strings.TrimSpace(out) == "" {
		fmt.Fprintln(stderr, "goldtool prereg: --digest needs --prereg FILE and --out FILE")
		return exitUsage
	}
	if strings.TrimSpace(unused) != "" {
		fmt.Fprintln(stderr, "goldtool prereg: --digest-file belongs to --check; --digest writes one")
		return exitUsage
	}
	p, err := readPrereg(preregPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
		return exitUsage
	}
	problems, _ := checkPrereg(p)
	if len(problems) > 0 {
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool prereg: %s\n", pr)
		}
		fmt.Fprintln(stderr, "goldtool prereg: refusing to digest a pre-registration that does not pass its own "+
			"checks: the digest would pin the breakage as the agreement")
		return exitInvariant
	}
	d, err := buildPreregDigest(p, preregPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
		return exitUsage
	}
	if err := writePreregDigest(out, d); err != nil {
		fmt.Fprintf(stderr, "goldtool prereg: %s already exists or cannot be written (%v): a digest that can be "+
			"rewritten in place records the current file, not the frozen one\n", out, err)
		return exitUsage
	}
	summarisePreregDigest(stdout, out, d)
	return exitOK
}
