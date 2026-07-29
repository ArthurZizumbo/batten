package hooks

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
)

func specFrom(t *testing.T, yaml string) *spec.Spec {
	t.Helper()
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "batten.yaml"), yaml); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	return sp
}

const orientHead = "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"

// TestOrientBeforeReadingIsTheChainThatWasNeverWired.
//
// `capabilities.graph.query_before_read` and `phases[].graph_query` promised, from the beginning,
// that an agent would ask the code graph before opening files. Both had ZERO consumers. So the one
// phase that writes code — the fan-out — consulted nothing and went straight to reading files,
// which is the most expensive of the three ways to find out what the code is.
func TestOrientBeforeReadingIsTheChainThatWasNeverWired(t *testing.T) {
	sp := specFrom(t, orientHead+
		"capabilities:\n  graph: { provider: graphify, query_before_read: true }\n"+
		"  memory: { provider: engram }\n"+
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    requires_verdict: ok\n")
	build, _ := sp.Phase("build")

	got := OrientBeforeReading(sp, &build)
	if got == "" {
		t.Fatal("query_before_read is true and the agent was told nothing")
	}
	// Both memories, and the ORDER: what the code IS, then what we DECIDED, then grep last.
	gi, ei := strings.Index(got, "graphify"), strings.Index(got, "engram")
	if gi < 0 || ei < 0 {
		t.Fatalf("the instruction names %q; it must name both providers:\n%s", got, got)
	}
	if gi > ei {
		t.Errorf("engram comes before graphify. The order is not decoration: graphify answers what "+
			"the code IS and is cheaper than searching decisions:\n%s", got)
	}
	if !strings.Contains(got, "grep") {
		t.Errorf("grep is not named as the fallback, so nothing says it is the expensive one:\n%s", got)
	}
	// THE HONESTY CLAUSE. An agent told to consult two tools will report having consulted them
	// whether or not they answered — that is the single most likely way an instruction of this
	// shape fails, and the instruction has to demand the opposite.
	if !strings.Contains(got, "say so") {
		t.Errorf("the instruction does not demand that an unavailable memory be DECLARED, so the "+
			"agent can simulate a consultation and nothing catches it:\n%s", got)
	}
}

// The phase-level flag alone is enough: `graph_query: true` on one phase and the capability off is
// a legitimate spec, and reading only the capability would ignore half the promise.
func TestThePhaseFlagAloneTurnsItOn(t *testing.T) {
	sp := specFrom(t, orientHead+
		"capabilities:\n  graph: { provider: graphify }\n"+
		"phases:\n  - id: build\n    graph_query: true\n  - id: close\n    requires_verdict: ok\n")
	build, _ := sp.Phase("build")
	if OrientBeforeReading(sp, &build) == "" {
		t.Error("`graph_query: true` on the phase was ignored")
	}

	// And a phase that declares neither says nothing at all — this must not become noise on
	// every phase of every spec.
	closing, _ := sp.Phase("close")
	if got := OrientBeforeReading(sp, &closing); got != "" {
		t.Errorf("a phase that declares neither flag was given the instruction anyway:\n%s", got)
	}
}

// Declared with nothing to consult. Said out loud rather than skipped: a phase asking for a
// consultation with no provider configured is a spec contradicting itself, and silence there is
// how the contradiction survives.
func TestAskingForAGraphThatIsNotConfiguredSaysSo(t *testing.T) {
	sp := specFrom(t, orientHead+
		"phases:\n  - id: build\n    graph_query: true\n  - id: close\n    requires_verdict: ok\n")
	build, _ := sp.Phase("build")

	got := OrientBeforeReading(sp, &build)
	if got == "" {
		t.Fatal("a phase asking to consult a graph with no provider configured got silence")
	}
	if !strings.Contains(got, "nothing to consult") {
		t.Errorf("the contradiction is not named:\n%s", got)
	}
}

// A nil phase must not panic: phaseBriefing calls this for whatever phase a run happens to be in,
// including one the spec does not declare.
func TestOrientHandlesAPhaseTheSpecDoesNotDeclare(t *testing.T) {
	sp := specFrom(t, orientHead+"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n")
	if got := OrientBeforeReading(sp, nil); got != "" {
		t.Errorf("a nil phase produced %q", got)
	}
}
