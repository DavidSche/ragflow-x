package httperr

import (
	"sort"
	"strings"
	"testing"
)

// TestBlocksAreDisjointOwnedAndDocumented is the shape check for the registry
// itself: a band is only a boundary if it is sorted, non-overlapping, and says
// who owns it. A block with an empty Owner or Doc is a comment, not a boundary.
func TestBlocksAreDisjointOwnedAndDocumented(t *testing.T) {
	if len(Blocks) == 0 {
		t.Fatal("no blocks declared")
	}
	sorted := make([]Block, len(Blocks))
	copy(sorted, Blocks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Min < sorted[j].Min })
	for i := range sorted {
		if sorted[i].Min != Blocks[i].Min {
			t.Fatalf("Blocks must stay ordered by Min: %v", Blocks)
		}
	}

	var problems []string
	seen := map[string]string{}
	for _, b := range Blocks {
		if b.Min > b.Max {
			problems = append(problems, "block "+itoa(b.Min)+"-"+itoa(b.Max)+" is inverted")
		}
		if strings.TrimSpace(b.Owner) == "" {
			problems = append(problems, "block "+itoa(b.Min)+" has no owner")
		}
		if strings.TrimSpace(b.Purpose) == "" {
			problems = append(problems, "block "+itoa(b.Min)+" has no purpose")
		}
		if strings.TrimSpace(b.Doc) == "" {
			problems = append(problems, "block "+itoa(b.Min)+" names no doc")
		}
		if previous, ok := seen[b.Owner]; ok && b.Owner != "" {
			problems = append(problems, "owner "+b.Owner+" owns both "+previous+" and "+itoa(b.Min)+"; one owner is one band")
		}
		seen[b.Owner] = itoa(b.Min)
	}
	for i := 1; i < len(Blocks); i++ {
		if Blocks[i].Min <= Blocks[i-1].Max {
			problems = append(problems, "blocks "+itoa(Blocks[i-1].Min)+"-"+itoa(Blocks[i-1].Max)+
				" and "+itoa(Blocks[i].Min)+"-"+itoa(Blocks[i].Max)+" overlap")
		}
	}
	if len(problems) > 0 {
		t.Fatalf("block registry is not a set of boundaries:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestEveryDeclaredCodeResolvesToItsOwnBlock is the rule the registry exists for.
// The `Declared()` table is the closed set of 41xxx codes; each one must resolve
// to the request-precondition band and to no other, and the band constants must
// agree with the registry row (a second place to edit is a second place to drift).
func TestEveryDeclaredCodeResolvesToItsOwnBlock(t *testing.T) {
	precondition, ok := BlockOf(BlockMin)
	if !ok {
		t.Fatalf("BlockMin %d is outside every band", BlockMin)
	}
	if precondition.Min != BlockMin || precondition.Max != BlockMax {
		t.Fatalf("BlockMin/BlockMax (%d/%d) drifted from the registry row %d-%d",
			BlockMin, BlockMax, precondition.Min, precondition.Max)
	}
	if precondition.Owner != "request-precondition" {
		t.Fatalf("BlockMin resolves to owner %q", precondition.Owner)
	}

	var problems []string
	for _, d := range Declared {
		owner, ok := BlockOf(d.Code)
		if !ok {
			problems = append(problems, itoa(d.Code)+" ("+d.Name+") is outside every declared band")
			continue
		}
		if owner.Owner != precondition.Owner {
			problems = append(problems, itoa(d.Code)+" ("+d.Name+") lives in band "+owner.Owner+", not request-precondition")
		}
	}
	if len(problems) > 0 {
		t.Fatalf("declared codes escaped their band:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestReservedBlocksStayEmpty is the "no code without a producer" rule (doc/319
// §8.7, and the R-01 lesson about `evidence_expired`). A granted band that has
// codes in it was not granted — it was used.
func TestReservedBlocksStayEmpty(t *testing.T) {
	reserved := ReservedBlocks()
	if len(reserved) == 0 {
		t.Fatal("no reserved band: doc/306's block should be registered before P0a, not during it")
	}
	for _, b := range reserved {
		for _, d := range Declared {
			if d.Code >= b.Min && d.Code <= b.Max {
				t.Fatalf("code %d (%s) was minted inside the reserved band %d-%d (%s) before the feature exists",
					d.Code, d.Name, b.Min, b.Max, b.Owner)
			}
		}
	}
}

// TestKnowledgeBlockIsGrantedBeforeP0a pins the decision this round implements:
// 306 gets a band of its own, so P0a's four endpoints do not have to squeeze into
// 441xx (which is full) or invent numbers inside 411xx (which would let a
// freshness refusal answer with a report sentence).
func TestKnowledgeBlockIsGrantedBeforeP0a(t *testing.T) {
	block, ok := BlockOf(46000)
	if !ok {
		t.Fatal("no band registered for 46000: doc/306 P0a cannot start")
	}
	if block.Owner != "knowledge-freshness" {
		t.Fatalf("46000 belongs to %q, expected knowledge-freshness", block.Owner)
	}
	if block.Min != 46000 || block.Max != 46999 {
		t.Fatalf("knowledge band is %d-%d, expected 46000-46999", block.Min, block.Max)
	}
	if !block.Reserved {
		t.Fatal("knowledge band must stay Reserved until P0a declares its first code")
	}
	// The report band must be visible as *not* the place for new knowledge codes.
	report, ok := BlockOf(44101)
	if !ok || report.Owner != "report" {
		t.Fatalf("44101 must resolve to the report band, got %+v (ok=%v)", report, ok)
	}
}

// TestCodesOutsideEveryBandAreRefused proves BlockOf has a failure direction: a
// code in the gap between two bands must not silently resolve to the nearest one.
func TestCodesOutsideEveryBandAreRefused(t *testing.T) {
	for _, code := range []int{41000, 41099, 43000, 44000, 45000, 47000, 49000, 51000, 1, 200} {
		if block, ok := BlockOf(code); ok {
			t.Fatalf("code %d resolved to %s (%d-%d); the gap must stay unowned",
				code, block.Owner, block.Min, block.Max)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
