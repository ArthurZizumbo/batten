// Package install holds the one path every batten hook invokes, and nothing else.
//
// # Why a package for two functions
//
// The distribution strategy rests on a single fact: Claude Code puts a plugin's `bin/` on PATH
// and the plugin's own manifests address the binary through ${CLAUDE_PLUGIN_ROOT}. So there is
// exactly one place a working batten can live — `<plugin root>/bin/batten[.exe]` — and four
// independent artifacts have to agree on it:
//
//   - plugin/claude-code/hooks/hooks.json — seven hook invocations
//   - plugin/claude-code/.mcp.json        — the MCP server command
//   - scripts/bootstrap.sh / .ps1         — what a release install actually writes
//   - `batten doctor`                     — what it inspects to answer "is the gate running?"
//
// They did not agree. bootstrap wrote the binary to ${CLAUDE_PLUGIN_DATA}/bin while every
// consumer read ${CLAUDE_PLUGIN_ROOT}/bin, so a release install printed "installed" and then
// no hook existed, the MCP server never started, and doctor reported "the gate is not running
// at all" about a machine where bootstrap had just succeeded. Four files spelling one contract
// is how that happens; one function they all defer to is how it stops.
package install

import (
	"path/filepath"
	"runtime"
)

// HookedBinRef is how hooks.json and .mcp.json must spell the binary: Claude Code expands
// ${CLAUDE_PLUGIN_ROOT} before it spawns the process. No extension — CreateProcess and libuv
// both append .exe for an extensionless path on Windows, and hard-coding it would break Unix.
const HookedBinRef = "${CLAUDE_PLUGIN_ROOT}/bin/batten"

// BinName is the executable's file name on this platform.
func BinName() string {
	if runtime.GOOS == "windows" {
		return "batten.exe"
	}
	return "batten"
}

// BinPath is the file every hook invokes, given the plugin's installed root.
//
// It is deliberately NOT "whatever batten is on PATH": the hooks name this file explicitly, so
// a batten elsewhere on PATH can be a different version — or the only one that exists — while
// the gate is not running. Any check that resolves through PATH cannot see that.
func BinPath(pluginRoot string) string {
	return filepath.Join(pluginRoot, "bin", BinName())
}
