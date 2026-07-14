package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A plugin CANNOT register a statusLine: it lives in settings.json, which belongs to the user.
// So installation is explicit and consented, and it is the one place batten writes to a file it
// does not own. Everything here is built around not damaging that file.
const (
	claudeDir    = ".claude"
	settingsName = "settings.json"

	// chainName holds the statusLine command batten displaced. It is a sidecar rather than a
	// key inside settings.json on purpose: Claude Code validates that file and complains about
	// keys it does not recognize, and a tool that makes the user's editor cry wolf gets
	// uninstalled. The sidecar is ours, so it can carry an explanation for whoever finds it.
	chainName = "batten-statusline-chain.json"
)

// statusLineKey is the settings.json key we own. Every other key in that file is somebody
// else's and must survive us byte-for-byte.
const statusLineKey = "statusLine"

type statusLineCfg struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type chainCfg struct {
	Command string `json:"command"`
	Note    string `json:"note"`
}

func settingsPath(projectDir string) string {
	return filepath.Join(projectDir, claudeDir, settingsName)
}

func chainPath(projectDir string) string {
	return filepath.Join(projectDir, claudeDir, chainName)
}

// IsBatten reports whether a statusLine command is one of ours. Used to make Install idempotent
// and, more importantly, to stop batten from chaining to itself: a self-chain would fork a new
// batten on every redraw, forever.
func IsBatten(command string) bool {
	l := strings.ToLower(command)
	return strings.Contains(l, "batten") && strings.Contains(l, "statusline")
}

// Installed reports whether a statusLine is already configured for this project, and what it is.
//
// A settings.json we cannot parse is an error, never an absence: reporting "nothing installed"
// for a file we failed to read is how a tool ends up overwriting a config it never understood.
func Installed(projectDir string) (present bool, existing string, err error) {
	s, err := readSettings(settingsPath(projectDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", nil
		}
		return false, "", err
	}
	raw, ok := s.vals[statusLineKey]
	if !ok {
		return false, "", nil
	}
	var cfg statusLineCfg
	if err := json.Unmarshal(raw, &cfg); err == nil && cfg.Command != "" {
		return true, cfg.Command, nil
	}
	// Some other shape of statusLine (a future type, or hand-written). We still report it as
	// present — hand it back verbatim so the caller can show the user exactly what is there.
	return true, strings.TrimSpace(string(raw)), nil
}

// Install writes {"statusLine":{"type":"command","command":"<batten> statusline"}} into
// <projectDir>/.claude/settings.json, preserving every other key in that file.
//
// If a statusLine already exists and chain is true, batten wraps it: batten prints its own
// segment and then appends the previous command's output. If one exists and chain is false,
// Install refuses rather than clobbering the user's configuration.
func Install(projectDir, battenPath string, chain bool) error {
	path := settingsPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	s, err := readSettings(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Refuse to touch a settings.json we could not parse. Rewriting it would mean
		// discarding whatever we failed to understand.
		return fmt.Errorf("batten: %s is not valid JSON, refusing to modify it: %w", path, err)
	}
	if s == nil {
		s = newSettings()
	}

	cmd := command(battenPath)

	if raw, ok := s.vals[statusLineKey]; ok {
		var cur statusLineCfg
		if err := json.Unmarshal(raw, &cur); err != nil || cur.Type != "command" || cur.Command == "" {
			// We cannot run what we cannot read, so we cannot promise to chain it either.
			return fmt.Errorf("batten: %s already has a statusLine we do not know how to wrap (%s). "+
				"Remove it or edit it by hand; batten will not guess", path, strings.TrimSpace(string(raw)))
		}
		switch {
		case IsBatten(cur.Command):
			// Already ours: re-point it at this binary and leave any existing chain alone.
			// Re-running install must be safe, and must never chain batten to batten.
		case !chain:
			return fmt.Errorf("batten: a statusLine is already configured in %s:\n  %s\n"+
				"batten will not overwrite it. Re-run with chaining to keep it: batten's segment is "+
				"printed first and that command's output is appended", path, cur.Command)
		default:
			if err := writeChain(projectDir, cur.Command); err != nil {
				return err
			}
		}
	}

	val, err := json.MarshalIndent(statusLineCfg{Type: "command", Command: cmd}, "  ", "  ")
	if err != nil {
		return err
	}
	s.set(statusLineKey, val)
	return s.write(path)
}

// command builds the shell command string Claude Code will run. The path is quoted when it
// contains spaces — "C:\Program Files\..." is the common case on the platform this tool is
// written on, and an unquoted one would silently execute the wrong thing.
func command(battenPath string) string {
	p := battenPath
	if p == "" {
		p = "batten"
	}
	if strings.ContainsAny(p, " \t") && !strings.HasPrefix(p, `"`) {
		p = `"` + p + `"`
	}
	return p + " statusline"
}

func writeChain(projectDir, prev string) error {
	if IsBatten(prev) {
		return nil // never chain batten to batten
	}
	b, err := json.MarshalIndent(chainCfg{
		Command: prev,
		Note: "The statusLine command batten wrapped. `batten statusline` runs it with the same " +
			"stdin payload and appends its output. Delete this file to stop chaining.",
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(chainPath(projectDir), append(b, '\n'), 0o644)
}

// chainedCommand returns the wrapped command, or "" when nothing is chained. An unreadable
// sidecar is treated as "no chain": this runs on the display path, where being quiet beats
// being right.
func chainedCommand(projectDir string) (string, error) {
	b, err := os.ReadFile(chainPath(projectDir))
	if err != nil {
		return "", nil //nolint:nilerr // absence (and damage) both mean "nothing to chain"
	}
	var c chainCfg
	if err := json.Unmarshal(b, &c); err != nil {
		return "", nil
	}
	return c.Command, nil
}

// ---------- settings.json, preserved ----------

// settings is settings.json held as ordered raw values.
//
// The obvious implementation — unmarshal into map[string]any, marshal it back — silently
// reorders every key, reformats every number (1e6 becomes 1000000, and integers become floats),
// and reindents the whole file. This is a config a human hand-edits. Keeping the key order and
// the exact bytes of every value we did not touch means our write shows up as a one-key diff.
type settings struct {
	keys []string
	vals map[string]json.RawMessage
}

func newSettings() *settings {
	return &settings{vals: map[string]json.RawMessage{}}
}

func (s *settings) set(key string, val json.RawMessage) {
	if _, ok := s.vals[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.vals[key] = val
}

func readSettings(path string) (*settings, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return newSettings(), nil // an empty file is an empty object, not a parse error
	}

	dec := json.NewDecoder(strings.NewReader(string(b)))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object, got %v", tok)
	}

	s := newSettings()
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("expected an object key, got %v", kt)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		s.set(key, raw)
	}
	if _, err := dec.Token(); err != nil { // consume '}'; a truncated file must not be "valid"
		return nil, err
	}
	return s, nil
}

// write replaces settings.json atomically. A half-written settings.json breaks every subsequent
// Claude Code session in the repo, so the file is only ever swapped in whole.
func (s *settings) write(path string) error {
	var b strings.Builder
	b.WriteString("{\n")
	for i, k := range s.keys {
		key, err := json.Marshal(k)
		if err != nil {
			return err
		}
		b.WriteString("  ")
		b.Write(key)
		b.WriteString(": ")
		b.Write(s.vals[k]) // verbatim: whatever the user wrote stays exactly as they wrote it
		if i < len(s.keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")

	tmp := path + ".batten.tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // Windows: rename over an open file can fail; do not leave litter behind
		return err
	}
	return nil
}
