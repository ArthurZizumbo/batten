package store

import (
	"testing"
)

// Ítem 21: "criteria" appeared ten times in the codebase's prose and zero times as
// data — evidence was a flat []string and nothing could say WHICH criterion a piece of
// evidence covered. These tests pin the data half: seeding from the plan document, and an
// approving verdict covering exactly the criteria its evidence cites.

func TestSeedCriteriaIsIdempotentAcrossPhases(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	texts := []string{"returns 429 over the limit", "the header names the window"}
	if err := s.SeedCriteria(r.RunID, "US-1", texts); err != nil {
		t.Fatal(err)
	}
	// Cover one, then re-seed — every `batten phase` passes through the seeder, and a
	// re-seed that wiped statuses would reset the scoreboard on every phase change.
	if err := s.SaveVerdict(Verdict{RunID: r.RunID, Gate: "qa", CheckID: "review", Result: "ok",
		Evidence: []string{"AC-1: curl -i shows 429 on request 11"}}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedCriteria(r.RunID, "US-1", texts); err != nil {
		t.Fatal(err)
	}

	cs, err := s.Criteria(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %d criteria, want 2 (re-seeding must not duplicate)", len(cs))
	}
	if cs[0].Status != StatusCovered || cs[0].Evidence == "" {
		t.Errorf("AC-1 = %+v, want covered with its citing evidence — the re-seed wiped it", cs[0])
	}
	if cs[1].Status != "open" {
		t.Errorf("AC-2 = %+v, want still open: nothing cited it", cs[1])
	}
}

func TestOnlyAnApprovingVerdictCoversWhatItCites(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")
	if err := s.SeedCriteria(r.RunID, "US-1", []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}

	// A blocked verdict naming AC-2 is describing what FAILED; marking it covered would
	// invert its meaning.
	if err := s.SaveVerdict(Verdict{RunID: r.RunID, Gate: "qa", CheckID: "review", Result: "blocked",
		Evidence: []string{"AC-2: fails — no header in the response"}}, true); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Criteria(r.RunID)
	for _, c := range cs {
		if c.Status != "open" {
			t.Errorf("AC-%d covered by a BLOCKED verdict: %+v", c.Idx, c)
		}
	}

	// The approving one covers exactly what it cites — and a citation to an index that was
	// never seeded covers nothing rather than erroring the save.
	if err := s.SaveVerdict(Verdict{RunID: r.RunID, Gate: "qa", CheckID: "review", Result: "ok",
		Evidence: []string{"AC-2: response carries Retry-After", "AC-9: cites nothing that exists",
			"go test ./...: PASS (uncited evidence is still evidence)"}}, true); err != nil {
		t.Fatal(err)
	}
	cs, _ = s.Criteria(r.RunID)
	want := map[int]string{1: "open", 2: StatusCovered, 3: "open"}
	for _, c := range cs {
		if c.Status != want[c.Idx] {
			t.Errorf("AC-%d = %s, want %s", c.Idx, c.Status, want[c.Idx])
		}
	}
}
