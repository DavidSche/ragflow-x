package ragflow

import "testing"

func TestCompilationStatusStateSetIsFrozen(t *testing.T) {
	want := []string{"idle", "pending", "running", "completed"}
	got := KnownCompilationStatusStates()
	if len(got) != len(want) {
		t.Fatalf("compilation state set = %v, want %v", got, want)
	}
	for index, state := range want {
		if got[index] != state {
			t.Fatalf("compilation state[%d] = %q, want %q", index, got[index], state)
		}
		if !IsKnownCompilationStatusState(state) {
			t.Fatalf("frozen state %q is not recognized", state)
		}
	}
	for _, state := range []string{"", "failed", "error", "stalled"} {
		if IsKnownCompilationStatusState(state) {
			t.Fatalf("diagnostic or unknown state %q must not be a lifecycle state", state)
		}
	}
}
