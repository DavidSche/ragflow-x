package engdocs

// ScenarioID: SC-DOC-005
//
// doc/specification/16 §7 freezes the S-2 seam (doc/319 §5.3): the words a user
// reads must be the words the server stores. Every closed set that crosses the
// HTTP boundary exists three times — a Go function, a TypeScript array, and two
// dictionaries — and until this gate existed **nothing compared them**.
//
// The failure this prevents has a specific shape. `R-01`/`R-02` (doc/319 §8) were
// possible because `evidence_expired` and `evidence_missing` were added to the
// server's quote-status set and to the panel, while the *rule* that decides which
// statuses skip the semantic layer was written once, by hand, in a third place. A
// value that reaches the panel without a dictionary entry renders as raw
// `snake_case` English inside a Chinese sentence — and the run that produced it
// is green, because no test renders the panel.
//
// What this gate checks, per set:
//  1. the TypeScript array and the Go closed set contain the same values;
//  2. every value has a key in `web/src/i18n/zh.ts` **and** `en.ts`;
//  3. where the dictionary is a pure block (verdicts, finding status, finding
//     category), the block contains nothing else — an unused key is a decision
//     nobody made, and it lets the next value look "already handled";
//  4. the documented frontend-only keys (`answerable`, `not_recorded`,
//     `quote_unchecked`) are declared here rather than tolerated: they are states
//     the UI can be in that the server never stores.
//
// What this gate is not: it does not run Node and it does not judge wording. Two
// Chinese sentences can be equally wrong.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

const (
	webReportAssurance = "web/src/resources/conversation-center/report/report-assurance.ts"
	webAnswerability   = "web/src/resources/workbench/answerability.ts"
	i18nZh             = "web/src/i18n/zh.ts"
	i18nEn             = "web/src/i18n/en.ts"
)

type vocabularySet struct {
	Name string
	File string
	// Constant is the exported `as const` array in the TypeScript file.
	Constant string
	// GoValues is the server's closed set.
	GoValues []string
	// Block is the i18n namespace that holds one key per value ("" = flat keys).
	Block string
	// Prefix is used when Block is empty: keys are `<prefix><value>`.
	Prefix string
	// Aliases maps a value to a different dictionary key, where the UI needs a
	// human-facing name instead of the stored token.
	Aliases map[string]string
	// FrontendOnly are dictionary keys in the same surface that the server never
	// stores. Declaring them keeps check (3) meaningful.
	FrontendOnly []string
	// SkipBidirectional drops check (3) for a block that also carries prose
	// (labels, tooltips, filters). The one-directional check still catches a value
	// without a sentence; what it cannot do there is prove a sentence has a value.
	SkipBidirectional bool
}

func vocabularySets() []vocabularySet {
	return []vocabularySet{
		{
			Name:     "quote status",
			File:     webReportAssurance,
			Constant: "QUOTE_STATUSES",
			GoValues: model.ReportQuoteStatuses(),
			Prefix:   "quote_",
			// The panel says "未检查引文" for a finding written before the quote was
			// checked at all; `quote_unchecked` is that sentence, not a status.
			FrontendOnly: []string{"unchecked"},
		},
		{
			Name:     "verdict",
			File:     webReportAssurance,
			Constant: "ASSURANCE_VERDICTS",
			GoValues: model.ReportVerdicts(),
			Block:    "verdict",
		},
		{
			Name:     "finding status",
			File:     webReportAssurance,
			Constant: "FINDING_STATUSES",
			GoValues: model.ReportFindingStatuses(),
			Block:    "finding_status",
		},
		{
			Name:     "finding category",
			File:     webReportAssurance,
			Constant: "FINDING_CATEGORIES",
			GoValues: model.ReportFindingCategories(),
			Block:    "finding_category",
		},
		{
			Name:     "answerability reason",
			File:     webAnswerability,
			Constant: "ANSWERABILITY_REASONS",
			GoValues: service.AnswerabilityReasons(),
			Block:    "answerability",
			// The server's `none` means "this turn was answered"; the badge's word
			// for that is 有依据回答, keyed `answerable`. The mapping is the point:
			// a second `none:` key would render the token instead of the sentence.
			Aliases:           map[string]string{"none": "answerable"},
			SkipBidirectional: true,
			// `not_recorded` is the turn that predates the field. The server cannot
			// store it, and rendering it as `none` would claim the turn answered.
			FrontendOnly: []string{"not_recorded"},
		},
	}
}

var tsArrayPattern = regexp.MustCompile(`(?s)export const %s = \[(.*?)\] as const;`)

var tsStringToken = regexp.MustCompile(`"([a-z0-9_]+)"`)

// parseTSArray reads a `… as const` string array. It is line-oriented on purpose:
// the arrays in these two files are literal tokens, and a general TypeScript
// parser here would be a second source of truth about how the tokens are written.
func parseTSArray(source, constant string) []string {
	pattern := regexp.MustCompile(strings.Replace(tsArrayPattern.String(), "%s", regexp.QuoteMeta(constant), 1))
	match := pattern.FindStringSubmatch(source)
	if match == nil {
		return nil
	}
	// Token-level, not line-level: `FINDING_STATUSES` is written on one line and
	// `QUOTE_STATUSES` one token per line, and a parser that only handles the layout
	// of the file it was written against goes blind the next time someone runs prettier.
	var values []string
	for _, token := range tsStringToken.FindAllStringSubmatch(match[1], -1) {
		values = append(values, token[1])
	}
	return values
}

// parseI18nBlock extracts the `key: "…"` names of one nested object. Parsing is
// limited to the block's own braces: `open` and `resolved` appear in a dozen other
// namespaces in the same file, and a global regex would happily "find" them there.
func parseI18nBlock(source, block string) []string {
	needle := block + ": {"
	start := strings.Index(source, needle)
	if start < 0 {
		return nil
	}
	depth := 0
	end := -1
	for index := start + len(block); index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = index
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	body := source[start+len(needle) : end]
	var keys []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \"'{") {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}

// parseFlatI18nKeys returns every `<prefix><key>` name in the file. Only used for
// prefixes that are unique to one surface (`quote_`), because the check is
// bidirectional and a shared prefix would collect another module's keys.
func parseFlatI18nKeys(source, prefix string) []string {
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(prefix) + `([a-z0-9_]+):`)
	var keys []string
	for _, match := range pattern.FindAllStringSubmatch(source, -1) {
		keys = append(keys, match[1])
	}
	return keys
}

func sortedCopy(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	sort.Strings(out)
	return out
}

func diffSets(left, right []string) (onlyLeft, onlyRight []string) {
	inRight := map[string]bool{}
	for _, value := range right {
		inRight[value] = true
	}
	inLeft := map[string]bool{}
	for _, value := range left {
		inLeft[value] = true
	}
	for _, value := range sortedCopy(left) {
		if !inRight[value] {
			onlyLeft = append(onlyLeft, value)
		}
	}
	for _, value := range sortedCopy(right) {
		if !inLeft[value] {
			onlyRight = append(onlyRight, value)
		}
	}
	return onlyLeft, onlyRight
}

// TestWebVocabularyMatchesServerClosedSets is the parity gate.
func TestWebVocabularyMatchesServerClosedSets(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	read := func(relative string) string {
		body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			t.Fatalf("read %s: %v", relative, readErr)
		}
		return string(body)
	}
	zh := read(i18nZh)
	en := read(i18nEn)

	for _, set := range vocabularySets() {
		t.Run(set.Name, func(t *testing.T) {
			if len(set.GoValues) == 0 {
				t.Fatalf("%s: the server closed set is empty, so parity would be trivially true", set.Name)
			}
			tsValues := parseTSArray(read(set.File), set.Constant)
			if len(tsValues) == 0 {
				t.Fatalf("%s: %s not found in %s", set.Name, set.Constant, set.File)
			}

			// (1) the two declarations agree, as sets: order is presentation.
			onlyTS, onlyGo := diffSets(tsValues, set.GoValues)
			if len(onlyTS) > 0 || len(onlyGo) > 0 {
				t.Fatalf("%s: %s and the server closed set disagree\n  only in TypeScript: %v\n  only in Go: %v",
					set.Name, set.Constant, onlyTS, onlyGo)
			}
			if duplicates := duplicateValues(tsValues); len(duplicates) > 0 {
				t.Fatalf("%s: %s repeats %v", set.Name, set.Constant, duplicates)
			}

			// (2) every value has a sentence in both languages, and (3) a pure block
			// has nothing else in it.
			for language, source := range map[string]string{"zh": zh, "en": en} {
				keys := set.keysIn(language, source)
				declared := map[string]bool{}
				for _, value := range append(append([]string{}, set.GoValues...), set.FrontendOnly...) {
					name := set.keyFor(value)
					declared[name] = true
					if !keys[name] {
						t.Fatalf("%s: %s.ts has no `%s` for value %q", set.Name, language, name, value)
					}
				}
				if set.Block == "" || set.SkipBidirectional {
					continue
				}
				if len(keys) == 0 {
					t.Fatalf("%s: %s.ts has no `%s` block", set.Name, language, set.Block)
				}
				for name := range keys {
					if !declared[name] {
						t.Fatalf("%s: %s.ts `%s.%s` is not a value of the server set nor a declared frontend-only key",
							set.Name, language, set.Block, name)
					}
				}
			}
		})
	}
}

// keysIn returns the dictionary keys available to a set, in one language.
func (set vocabularySet) keysIn(language, source string) map[string]bool {
	keys := map[string]bool{}
	if set.Block == "" {
		for _, key := range parseFlatI18nKeys(source, set.Prefix) {
			keys[key] = true
		}
		return keys
	}
	for _, key := range parseI18nBlock(source, set.Block) {
		keys[key] = true
	}
	return keys
}

func (set vocabularySet) keyFor(value string) string {
	if alias, ok := set.Aliases[value]; ok {
		return alias
	}
	if set.Block == "" {
		return value // the flat-prefix form is matched by suffix
	}
	return value
}

func duplicateValues(values []string) []string {
	counts := map[string]int{}
	for _, value := range values {
		counts[value]++
	}
	var duplicates []string
	for value, count := range counts {
		if count > 1 {
			duplicates = append(duplicates, value)
		}
	}
	sort.Strings(duplicates)
	return duplicates
}
