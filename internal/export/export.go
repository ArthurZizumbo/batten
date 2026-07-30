// Package export renders a run to its human-facing surfaces (the JSON Canvas and, when a vault
// is configured, the Obsidian run note + dashboards). It exists so the same rendering can fire
// from three places without duplication: the `canvas` command, the Stop hook (auto), and after
// a verdict is saved (the moment the vault most wants to reflect the gate state).
package export

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/canvas"
	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
	"github.com/ArthurZizumbo/batten/internal/vault"
)

// VaultPath is the effective Obsidian vault: BATTEN_VAULT wins over the spec, and a leading ~
// expands to the home directory.
//
// The override exists for the same reason BATTEN_DB does. `batten.yaml` is committed, and in a
// public repo it is also the canonical example — so a vault path in it publishes one person's
// folder layout to everyone who reads the project, and hands every cloner a spec that writes into
// a directory they do not have. The personal path belongs in the environment; the spec carries the
// shape.
//
// Every surface resolves through here. Six call sites used to read spec.Capabilities.Obsidian.Vault
// directly, which is how `doctor` and the Stop hook end up disagreeing about whether a vault is
// configured at all.
func VaultPath(sp *spec.Spec) string {
	if sp == nil {
		return ""
	}
	if v := strings.TrimSpace(os.Getenv("BATTEN_VAULT")); v != "" {
		return expandHome(v)
	}
	return expandHome(sp.Capabilities.Obsidian.Vault)
}

// VaultWriter builds a vault writer for a spec.
func VaultWriter(sp *spec.Spec) *vault.Writer {
	return vault.New(VaultPath(sp), sp.Project)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// Result reports where things were written, for a caller that wants to tell the user.
type Result struct {
	CanvasPath   string
	RunNotePath  string // "" when no vault is configured
	Nodes, Edges int
}

// Run renders unitID's active run. With a vault configured it writes the canvas into the vault
// (beside the note that embeds it) plus the note and the dashboards; otherwise it drops the
// canvas in .batten/. Best-effort by contract: callers on the hook path must ignore the error,
// because export must never be able to break a session.
func Run(sp *spec.Spec, st *store.Store, unitID string) (*Result, error) {
	// Latest, not active: the Stop hook exports AFTER work concludes, and a just-closed run
	// is exactly the one whose note and canvas the vault should reflect.
	run, err := st.LatestRun(sp.Project, unitID)
	if err != nil {
		return nil, err
	}
	nodes, err := st.Nodes(run.RunID)
	if err != nil {
		return nil, err
	}
	edges, err := st.Edges(run.RunID)
	if err != nil {
		return nil, err
	}
	// Both verdicts, kept apart by producer. Reading only the latest row made `batten check`
	// the author of the note's verdict section: its source=batten row is always the newest, so
	// the reviewer's evidence was painted over with check output and the property the "needs a
	// human" dashboard reads said `ok` for a run nobody had reviewed. This is the third site of
	// that defect — `batten show` and the TUI were fixed first.
	rv, _ := st.LatestVerdictNotBySource(run.RunID, "", "batten")
	bv, _ := st.LatestVerdictBySource(run.RunID, "", "batten")
	ov, _ := st.OverrideFor(run.RunID, sp.ClosingGateName())

	res := &Result{}
	c := canvas.Render(run, nodes, edges, rv, bv, ov)
	res.Nodes, res.Edges = len(c.Nodes), len(c.Edges)

	if vlt := VaultPath(sp); vlt != "" {
		// `export:` chooses WHICH files land in the vault. An empty list keeps the historical
		// default — everything — but a user who wrote `export: [canvas]` was, until now, getting
		// all three anyway: the field was the guard's tenth instance, declared and never read.
		exports := sp.Capabilities.Obsidian.Export
		wants := func(kind string) bool {
			if len(exports) == 0 {
				return true
			}
			for _, e := range exports {
				if e == kind {
					return true
				}
			}
			return false
		}

		w := VaultWriter(sp)
		// The claims the fan-out actually made. Best-effort like the usage read below: a failure
		// here leaves WriteSets nil, and the note then says "not recorded" — which is the honest
		// reading of a query that did not answer.
		w.WriteSets, _ = st.WriteSetsByRun(run.RunID)
		canvasRel := ""
		if wants("canvas") {
			res.CanvasPath = w.CanvasPath(unitID)
			if err := c.WriteFile(res.CanvasPath); err != nil {
				return res, err
			}
			// Only a canvas that exists gets embedded: a note linking a file the export list
			// excluded would be a broken link shipped on purpose.
			canvasRel = w.CanvasRel(unitID)
		}
		if wants("runs") {
			usg, _ := st.UsageByNode(run.RunID)
			if err := w.WriteRun(run, nodes, edges, rv, bv, usg, canvasRel); err != nil {
				return res, err
			}
			res.RunNotePath = w.RunNotePath(unitID)
		}
		if wants("verdicts") {
			// The .base dashboards are where verdicts surface as their own artifact — the
			// "needs a human" list is a query over verdict properties.
			if err := w.WriteBases(); err != nil {
				return res, err
			}
		}
		return res, nil
	}

	res.CanvasPath = filepath.Join(sp.Root, ".batten", unitID+".canvas")
	return res, c.WriteFile(res.CanvasPath)
}
