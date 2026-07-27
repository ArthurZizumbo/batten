// Package export renders a run to its human-facing surfaces (the JSON Canvas and, when a vault
// is configured, the Obsidian run note + dashboards). It exists so the same rendering can fire
// from three places without duplication: the `canvas` command, the Stop hook (auto), and after
// a verdict is saved (the moment the vault most wants to reflect the gate state).
package export

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/arthu/batten/internal/canvas"
	"github.com/arthu/batten/internal/spec"
	"github.com/arthu/batten/internal/store"
	"github.com/arthu/batten/internal/vault"
)

// VaultWriter builds a vault writer for a spec, expanding a leading ~ in the vault path.
func VaultWriter(sp *spec.Spec) *vault.Writer {
	return vault.New(expandHome(sp.Capabilities.Obsidian.Vault), sp.Project)
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
	v, _ := st.LatestVerdict(run.RunID, "")

	res := &Result{}
	c := canvas.Render(run, nodes, edges, v)
	res.Nodes, res.Edges = len(c.Nodes), len(c.Edges)

	if vlt := sp.Capabilities.Obsidian.Vault; vlt != "" {
		w := VaultWriter(sp)
		// The claims the fan-out actually made. Best-effort like the usage read below: a failure
		// here leaves WriteSets nil, and the note then says "not recorded" — which is the honest
		// reading of a query that did not answer.
		w.WriteSets, _ = st.WriteSetsByRun(run.RunID)
		res.CanvasPath = w.CanvasPath(unitID)
		if err := c.WriteFile(res.CanvasPath); err != nil {
			return res, err
		}
		usg, _ := st.UsageByNode(run.RunID)
		if err := w.WriteRun(run, nodes, edges, v, usg, w.CanvasRel(unitID)); err != nil {
			return res, err
		}
		if err := w.WriteBases(); err != nil {
			return res, err
		}
		res.RunNotePath = w.RunNotePath(unitID)
		return res, nil
	}

	res.CanvasPath = filepath.Join(sp.Root, ".batten", unitID+".canvas")
	return res, c.WriteFile(res.CanvasPath)
}
