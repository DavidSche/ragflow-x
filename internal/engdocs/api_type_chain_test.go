package engdocs

// ScenarioID: SC-DOC-004
//
// doc/specification/16 §5 freezes the S-6 seam (doc/319 §5): the frontend's view of the API
// has exactly one generation chain —
//
//     版本化 OpenAPI (internal/docs/swagger.json)  →  web/scripts/api-types.mjs
//       →  web/src/types/openapi.d.ts (受控基线)  →  web/src/types/api.ts (唯一别名层)
//
// The failure this prevents is not "types are wrong"; it is **two answers to one question**.
// A second script that also writes `openapi.d.ts` (a quick fix, a different flag set, a
// "temporary" generator in a test setup) leaves the repo with two producers whose outputs
// disagree only sometimes — and `--check` then goes red for reasons nobody can reproduce,
// which is how a real check gets commented out.
//
// The same lesson as doc/322 R-04, stated for build tooling: one assembly point, and the
// guard has to consume the production one (the package.json script and the file on disk),
// not a copy the test keeps in sync by hand.
//
// What this gate is not: it does not run Node. `--check` in CI is the thing that compares
// bytes; this test is what stops a *second* chain from existing before anyone notices.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	apiTypeGeneratorScript   = "web/scripts/api-types.mjs"
	apiTypeControlledBase    = "web/src/types/openapi.d.ts"
	apiTypeAliasLayer        = "web/src/types/api.ts"
	apiTypeSpecSource        = "internal/docs/swagger.json"
	apiTypePackageJSON       = "web/package.json"
	apiTypeGeneratorModule   = "openapi-typescript"
	apiTypeSkipDirsMarkerTxt = "node_modules"
)

// TestApiTypeChainHasOneGenerator asserts S-6-a/c/d: one generator, one controlled baseline,
// one alias layer.
func TestApiTypeChainHasOneGenerator(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Skipf("module root not found: %v", err)
	}

	// S-6-a: the generator exists and still reads the versioned document. If either string
	// moves, the chain changed shape and this freeze needs a deliberate edit rather than a
	// quiet pass.
	generatorPath := filepath.Join(root, filepath.FromSlash(apiTypeGeneratorScript))
	generator, err := os.ReadFile(generatorPath)
	if err != nil {
		t.Fatalf("read %s: %v — S-6-a names this file as the only generator", apiTypeGeneratorScript, err)
	}
	if !strings.Contains(string(generator), apiTypeSpecSource) {
		t.Fatalf("%s no longer reads %s: either the chain's input moved (update doc/specification/16 §5) "+
			"or the generator was replaced", apiTypeGeneratorScript, apiTypeSpecSource)
	}
	// S-6-b: the compare-only mode must exist. Without it, "is the baseline current" can
	// only be answered by regenerating and looking at `git diff`, which is a habit, not a gate.
	if !strings.Contains(string(generator), `"--check"`) {
		t.Fatalf("%s lost its --check mode (S-6-b): a baseline that can only be refreshed, "+
			"never compared, is not a controlled baseline", apiTypeGeneratorScript)
	}

	// S-6-a, the "one" half: no second file under web/ may produce the same artifact.
	// The scan looks for the *module import* rather than a filename, because a second chain
	// is free to write from anywhere — what it cannot do without `openapi-typescript` is
	// produce the same output.
	var secondChain []string
	webRoot := filepath.Join(root, "web")
	err = filepath.Walk(webRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := info.Name()
		if info.IsDir() {
			if name == apiTypeSkipDirsMarkerTxt || name == ".next" || name == "dist" ||
				name == "coverage" || name == "test-results" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only source-ish files: a lockfile legitimately names the module.
		switch filepath.Ext(name) {
		case ".mjs", ".js", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		default:
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if relative == apiTypeGeneratorScript || strings.HasSuffix(relative, ".d.ts") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(body)
		if strings.Contains(text, `from "`+apiTypeGeneratorModule+`"`) ||
			strings.Contains(text, `from "`+apiTypeGeneratorModule+`/`) ||
			strings.Contains(text, `require("`+apiTypeGeneratorModule+`")`) {
			secondChain = append(secondChain, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk web/: %v", err)
	}
	if len(secondChain) > 0 {
		t.Fatalf("a second OpenAPI→TypeScript chain exists: %v\n"+
			"S-6-a allows one (%s). Two generators produce two answers to one question, and the "+
			"`--check` red that follows is unreproducible — which is how the check gets disabled.",
			secondChain, apiTypeGeneratorScript)
	}

	// S-6-c: the controlled baseline is in the repository. A generated file that is not
	// committed cannot be diffed, and a diff is the only thing that makes a rename visible.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(apiTypeControlledBase))); err != nil {
		t.Fatalf("%s is missing: S-6-c requires the generated artifact to be committed so a "+
			"backend rename shows up as a diff instead of a runtime surprise", apiTypeControlledBase)
	}

	// S-6-d: exactly one alias layer. `api.ts` is allowed to give the Go-qualified schema
	// keys readable names; nothing else in web/src may import the raw artifact, or the
	// renames would be applied twice, in two vocabularies.
	importers, err := apiTypeImporters(t, root)
	if err != nil {
		t.Fatalf("scan web/src for importers of the raw artifact: %v", err)
	}
	if len(importers) != 1 || importers[0] != apiTypeAliasLayer {
		t.Fatalf("the generated artifact is imported by %v, want exactly [%s]\n"+
			"S-6-d: one alias layer, otherwise a schema key gets two readable names and the one "+
			"that wins depends on the import graph.", importers, apiTypeAliasLayer)
	}

	// S-6-a, wiring half: package.json may have exactly two entries that touch the chain —
	// `gen:types` (write) and `check:types` (compare only) — and both must call *this*
	// generator. Two entry points are fine because they are one chain with two verbs; a
	// third, or one that calls another file, is the two-answers defect in package.json form.
	scripts, err := apiTypePackageScripts(t, root)
	if err != nil {
		t.Fatalf("%v", err)
	}
	allowed := map[string]bool{"gen:types": true, "check:types": true}
	var unexpected []string
	for name, command := range scripts {
		if !strings.Contains(command, "api-types") {
			continue
		}
		if !allowed[name] {
			unexpected = append(unexpected, name+" = "+command)
			continue
		}
		if !strings.Contains(command, "scripts/api-types.mjs") {
			t.Fatalf("web/package.json %q runs %q; it must call %s",
				name, command, apiTypeGeneratorScript)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("web/package.json has API-type scripts beyond gen:types/check:types: %v\n"+
			"S-6-a: write and compare are two verbs of one chain, not two chains.", unexpected)
	}
	for name, wantCheck := range map[string]bool{"gen:types": false, "check:types": true} {
		command, ok := scripts[name]
		if !ok {
			t.Fatalf("web/package.json lost the %q script", name)
		}
		// The verbs must stay distinct: a `check:types` that writes is a check nobody can
		// trust (it would repair the very drift it reports), and a `gen:types` that only
		// compares would never produce the baseline.
		if gotCheck := strings.Contains(command, "--check"); gotCheck != wantCheck {
			t.Fatalf("web/package.json %q = %q: --check presence must be %v", name, command, wantCheck)
		}
	}
}

// apiTypeImporters returns the repo-relative files under web/src that import the generated
// artifact. Both the extensionless and the explicit-extension spellings count: a chain that
// escaped through the other spelling is still a second chain.
func apiTypeImporters(t *testing.T, root string) ([]string, error) {
	t.Helper()
	var importers []string
	srcRoot := filepath.Join(root, "web", "src")
	err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		switch filepath.Ext(info.Name()) {
		case ".ts", ".tsx", ".mts", ".cts":
		default:
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if relative == apiTypeControlledBase {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(body)
		if strings.Contains(text, `from "./openapi"`) || strings.Contains(text, `from "./openapi.d.ts"`) ||
			strings.Contains(text, `from "./types/openapi"`) || strings.Contains(text, `from "../types/openapi"`) ||
			strings.Contains(text, `from "@/types/openapi"`) || strings.Contains(text, `types/openapi.d.ts`) {
			importers = append(importers, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Sorted for a stable message; the walk order is filesystem-dependent.
	for i := 1; i < len(importers); i++ {
		for j := i; j > 0 && importers[j] < importers[j-1]; j-- {
			importers[j], importers[j-1] = importers[j-1], importers[j]
		}
	}
	return importers, nil
}

func apiTypePackageScripts(t *testing.T, root string) (map[string]string, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(apiTypePackageJSON)))
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, err
	}
	return pkg.Scripts, nil
}
