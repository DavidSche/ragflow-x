package httperr

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDeclaredCodesAreUniqueAndInsideBlock is the registry invariant: one code,
// one meaning, inside the block. It is what lets the web client translate a
// code without also reading the free-text detail.
func TestDeclaredCodesAreUniqueAndInsideBlock(t *testing.T) {
	t.Parallel()
	seenCode := map[int]string{}
	seenName := map[string]int{}
	for _, d := range Declared {
		if d.Code < BlockMin || d.Code > BlockMax {
			t.Errorf("code %d (%s) is outside the %d-%d block", d.Code, d.Name, BlockMin, BlockMax)
		}
		if prev, ok := seenCode[d.Code]; ok {
			t.Errorf("code %d declared twice: %s and %s", d.Code, prev, d.Name)
		}
		seenCode[d.Code] = d.Name
		if prev, ok := seenName[d.Name]; ok {
			t.Errorf("name %s declared twice (codes %d and %d)", d.Name, prev, d.Code)
		}
		seenName[d.Name] = d.Code
		if strings.TrimSpace(d.Meaning) == "" {
			t.Errorf("code %d (%s) has no meaning", d.Code, d.Name)
		}
	}
}

// TestEveryBlockConstantIsRegistered catches the cheap mistake: a constant is
// added to codes.go but nobody registers it, so it never reaches the docs or
// the client table.
func TestEveryBlockConstantIsRegistered(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("codes.go")
	if err != nil {
		t.Fatalf("read codes.go: %v", err)
	}
	declared := map[int]bool{}
	for _, d := range Declared {
		declared[d.Code] = true
	}
	constRe := regexp.MustCompile(`(?m)^\s*(Code[A-Za-z]+)\s*=\s*(\d+)\s*(?://.*)?$`)
	found := 0
	for _, m := range constRe.FindAllStringSubmatch(string(src), -1) {
		value, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("parse %s: %v", m[1], err)
		}
		found++
		if !declared[value] {
			t.Errorf("constant %s = %d is not registered in Declared", m[1], value)
		}
	}
	if found != len(Declared) {
		t.Errorf("codes.go declares %d constants but Declared has %d rows", found, len(Declared))
	}
}

// Test40085HasASingleOwner guards the regression that started this split: the
// web client maps 40085 to one sentence, so exactly one call site may emit it.
// If a future feature wants 40085, it is adding a semantic — allocate a code in
// the 411xx block instead (doc/specification/12 §2.2).
func Test40085HasASingleOwner(t *testing.T) {
	t.Parallel()
	const (
		owner = "internal/service/role.go"
		// registry documents the split; it names the old code in prose only,
		// never in a call site.
		registry   = "internal/pkg/httperr/codes.go"
		overloaded = "40085"
	)
	// prose names a code without emitting it. Swagger's generated artifacts repeat every
	// `@Failure` description verbatim, and the hand-written contract file is exactly that
	// description; counting their mentions as leaks made the guard fire on the document
	// that is supposed to name the code (doc/310 T310-05). A call site in either file is
	// not a thing that exists — they hold annotations and string literals, no handlers.
	prose := []string{"internal/docs/", "internal/handler/openapi_routes_contracts.go"}
	root := filepath.Join("..", "..", "..")
	scanRoots := []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")}
	re := regexp.MustCompile(`\b40085\b`)

	counts := map[string]int{}
	for _, scanRoot := range scanRoots {
		err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if n := len(re.FindAll(body, -1)); n > 0 {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				rel = filepath.ToSlash(rel)
				if rel == registry {
					return nil
				}
				for _, prefix := range prose {
					if strings.HasPrefix(rel, prefix) || rel == prefix {
						return nil
					}
				}
				counts[rel] += n
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", scanRoot, err)
		}
	}

	for file, n := range counts {
		if file != owner {
			t.Errorf("%s carries %d occurrence(s) of %s; only %s may emit it — allocate a code in the %d-%d block instead",
				file, n, overloaded, owner, BlockMin, BlockMax)
		}
	}
	if counts[owner] != 1 {
		t.Errorf("%s should emit %s exactly once, found %d", owner, overloaded, counts[owner])
	}
}

// TestRoleEmitsOnlyItsOwnBlockCodes keeps the Role surface from drifting back
// onto codes that already mean something else. The Role block is 40080-40086
// (doc/specification/12 §2.2); everything else a role request can refuse —
// cross-scope parent, foreign parent, children in the way, a rule outside the
// catalog — is a 4116x precondition and must be a registered constant.
//
// Written after a role edit answered 40088 ("智能体 DSL 不能为空") for a parent
// that sat in the wrong scope: the client translated a well-argued refusal into
// a sentence about agents, because nothing stopped the service from borrowing
// the number.
func TestRoleEmitsOnlyItsOwnBlockCodes(t *testing.T) {
	t.Parallel()
	files := []string{
		filepath.Join("..", "..", "service", "role.go"),
		filepath.Join("..", "..", "handler", "role.go"),
	}
	// `httperr.BadRequest(40088, ...)` / `response.Fail(c, 40088, ...)`: a bare
	// number in the first argument position.
	re := regexp.MustCompile(`(?:BadRequest|Fail|Err)\((?:[a-zA-Z]+, )?(\d{5})`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(body), -1) {
			code, convErr := strconv.Atoi(m[1])
			if convErr != nil {
				t.Fatalf("parse %s: %v", m[1], convErr)
			}
			// 40000 / 40060 are the two documented generic buckets whose detail
			// carries the field name (doc/specification/12 §2.2), so they are
			// fine anywhere; anything else has to be a Role-block code.
			if code == 40000 || code == 40060 {
				continue
			}
			if code < 40080 || code > 40086 {
				t.Errorf("%s emits bare code %d; the Role block is 40080-40086 — allocate a 4116x constant in Declared instead",
					filepath.ToSlash(file), code)
			}
		}
	}
}
