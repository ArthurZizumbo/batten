package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// These tests RUN scripts/bootstrap.sh. That is the point: the release path had been "verified by
// reading it", and reading it is what missed that the binary landed somewhere no hook invokes.
//
// Only the download source is faked — a local server serving a real tar.gz that holds a real
// cross-compiled batten, shaped exactly like the GoReleaser asset. Everything else (curl, tar, the
// asset-name template, the chmod, the `batten version` self-check) is the shipping code path.

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
	buildDir  string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// realBinary compiles ./cmd/batten once per test binary. A fake payload would not do: bootstrap
// proves an install by running `batten version`, and that check is half of what is under test.
func realBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "batten-install-test-")
		if buildErr != nil {
			return
		}
		out := filepath.Join(buildDir, BinName())
		// The release flags, so the payload is the size and shape of a real asset.
		cmd := exec.Command("go", "build", "-ldflags", "-s -w", "-o", out, "./cmd/batten")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/batten: %v\n%s", err, b)
			return
		}
		builtBin = out
	})
	if buildErr != nil {
		t.Fatalf("could not build the payload binary: %v", buildErr)
	}
	return builtBin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// assetName is what GoReleaser's archives.name_template produces. bootstrap.sh has to ask for
// exactly one of these six names, and the server below refuses anything else — the "one contract
// in two files" check, executed instead of grepped.
var assetName = regexp.MustCompile(`^/batten_(linux|darwin|windows)_(amd64|arm64)\.tar\.gz$`)

// serveRelease stands in for releases/latest/download. It records what was asked for.
func serveRelease(t *testing.T, tgz []byte) (*httptest.Server, *string) {
	t.Helper()
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		if !assetName.MatchString(r.URL.Path) {
			http.Error(w, "no such asset", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tgz)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// releaseArchive builds the GoReleaser-shaped archive: the binary at the root, alongside the
// files .goreleaser.yaml attaches. bootstrap must pick the binary out of a multi-entry archive.
//
// Built once — gzipping a 14 MB binary per test is the whole runtime of this package.
func releaseArchive(t *testing.T) []byte {
	t.Helper()
	archiveOnce.Do(func() { archive = buildArchive(t) })
	return archive
}

var (
	archiveOnce sync.Once
	archive     []byte
)

func buildArchive(t *testing.T) []byte {
	t.Helper()
	bin, err := os.ReadFile(realBinary(t))
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	add := func(name string, mode int64, body []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}
	add(BinName(), 0o755, bin)
	add("README.md", 0o644, []byte("# batten\n"))
	add("LICENSE", 0o644, []byte("MIT\n"))
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// runBootstrap runs the shipping script against throwaway ROOT/DATA directories.
//
// HOME and BATTEN_DB are redirected even though this script touches neither: a bootstrap test
// that can reach the author's ~/.batten is one edit away from writing to it.
func runBootstrap(t *testing.T, root, data, baseURL string, extraPath ...string) (string, int) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH; bootstrap.sh cannot be exercised here")
	}
	script := filepath.Join(repoRoot(t), "scripts", "bootstrap.sh")

	home := t.TempDir()
	env := append(os.Environ(),
		"CLAUDE_PLUGIN_ROOT="+root,
		"CLAUDE_PLUGIN_DATA="+data,
		"BATTEN_BOOTSTRAP_BASE_URL="+baseURL,
		"HOME="+home,
		"BATTEN_DB="+filepath.Join(home, "test.db"),
	)
	if len(extraPath) > 0 {
		env = append(env, "PATH="+strings.Join(append(extraPath, os.Getenv("PATH")), string(os.PathListSeparator)))
	}

	cmd := exec.Command(bash, script)
	cmd.Env = env
	cmd.Dir = t.TempDir() // never the repo: a stray write must not land in the working tree
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", script, err)
	}
	return string(out), code
}

func mustRun(t *testing.T, bin string) {
	t.Helper()
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("%s does not exist — every hook invokes this file, so the gate is not running: %v", bin, err)
	}
	if b, err := exec.Command(bin, "version").CombinedOutput(); err != nil {
		t.Fatalf("%s does not run: %v\n%s", bin, err, b)
	}
}

func TestBootstrapInstallsWhereEveryHookLooks(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, asked := serveRelease(t, releaseArchive(t))

	out, code := runBootstrap(t, root, data, srv.URL)
	if code != 0 {
		t.Fatalf("bootstrap exited %d; a bootstrap must never break a session\n%s", code, out)
	}

	// The whole finding, in one assertion: the file hooks.json names has to exist and run.
	mustRun(t, BinPath(root))

	if !assetName.MatchString(*asked) {
		t.Errorf("bootstrap asked for %q, which is not a name .goreleaser.yaml builds", *asked)
	}
	if !strings.Contains(out, root) {
		t.Errorf("bootstrap did not say where it installed to; output was:\n%s", out)
	}
}

func TestBootstrapCachesTheDownloadOutsideThePluginRoot(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, _ := serveRelease(t, releaseArchive(t))

	if _, code := runBootstrap(t, root, data, srv.URL); code != 0 {
		t.Fatalf("bootstrap exited %d", code)
	}
	// ${CLAUDE_PLUGIN_DATA} survives a plugin update; ${CLAUDE_PLUGIN_ROOT} does not. Without
	// this copy, every update costs another 14 MB download.
	if _, err := os.Stat(filepath.Join(data, "bin", BinName())); err != nil {
		t.Errorf("the download was not cached in %s: %v", data, err)
	}
}

func TestBootstrapRestoresFromTheCacheAfterAPluginUpdate(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()

	// The post-update state: bin/ ships empty, so the update wiped the binary, and the only copy
	// left is the cache from the previous install.
	cacheDir := filepath.Join(data, "bin")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, realBinary(t), filepath.Join(cacheDir, BinName()))

	// A dead base URL: if bootstrap reaches for the network here, the test fails rather than
	// passing slowly for the wrong reason.
	out, code := runBootstrap(t, root, data, "http://127.0.0.1:1/unreachable")
	if code != 0 {
		t.Fatalf("bootstrap exited %d\n%s", code, out)
	}
	mustRun(t, BinPath(root))
}

// The short-circuit that made "installed" a lie: `command -v batten` is satisfied by a binary the
// hooks never invoke, so bootstrap declared victory over an empty plugin bin/.
func TestBootstrapIgnoresABattenOnPathThatIsNotTheHookedFile(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, _ := serveRelease(t, releaseArchive(t))

	elsewhere := t.TempDir()
	copyFile(t, realBinary(t), filepath.Join(elsewhere, BinName()))

	out, code := runBootstrap(t, root, data, srv.URL, elsewhere)
	if code != 0 {
		t.Fatalf("bootstrap exited %d\n%s", code, out)
	}
	mustRun(t, BinPath(root))
}

// The exit code carries meaning: hooks.json falls back to bootstrap.ps1 with `||`, so a failed
// DOWNLOAD must not look like a missing bash.
func TestBootstrapExitsZeroAndLeavesNothingBehindWhenTheDownloadFails(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, _ := serveRelease(t, []byte("404 not a tarball"))

	out, code := runBootstrap(t, root, data, srv.URL+"/wrong-prefix")
	if code != 0 {
		t.Fatalf("bootstrap exited %d; a failed download must not break the session\n%s", code, out)
	}
	if _, err := os.Stat(BinPath(root)); err == nil {
		t.Error("a half-installed binary was left in the plugin root; the hooks would run it")
	}
	if _, err := os.Stat(filepath.Join(data, "bin", BinName())); err == nil {
		t.Error("a half-installed binary was left in the cache; the next update would restore it")
	}
	if !strings.Contains(out, "nothing is being gated") {
		t.Errorf("a failed install must say so in plain words; output was:\n%s", out)
	}
}

// --- the Windows path, which had no script at all ---

// runBootstrapPS runs bootstrap.ps1 the way hooks.json's fallback does. Windows is the declared
// primary target, and until this script existed the only way to install there was through a bash
// a Windows machine is not required to have.
func runBootstrapPS(t *testing.T, root, data, baseURL string) (string, int) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("bootstrap.ps1 is the Windows path")
	}
	ps, err := exec.LookPath("powershell")
	if err != nil {
		if ps, err = exec.LookPath("pwsh"); err != nil {
			t.Skip("no powershell on PATH")
		}
	}
	script := filepath.Join(repoRoot(t), "scripts", "bootstrap.ps1")

	home := t.TempDir()
	cmd := exec.Command(ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Env = append(os.Environ(),
		"CLAUDE_PLUGIN_ROOT="+root,
		"CLAUDE_PLUGIN_DATA="+data,
		"BATTEN_BOOTSTRAP_BASE_URL="+baseURL,
		"USERPROFILE="+home,
		"BATTEN_DB="+filepath.Join(home, "test.db"),
	)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", script, err)
	}
	return string(out), code
}

func TestBootstrapPS1InstallsWhereEveryHookLooks(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, asked := serveRelease(t, releaseArchive(t))

	out, code := runBootstrapPS(t, root, data, srv.URL)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("bootstrap.ps1 said:\n%s", out)
		}
	})
	if code != 0 {
		t.Fatalf("bootstrap.ps1 exited %d; a bootstrap must never break a session\n%s", code, out)
	}
	mustRun(t, BinPath(root))
	if !assetName.MatchString(*asked) {
		t.Errorf("bootstrap.ps1 asked for %q, which is not a name .goreleaser.yaml builds", *asked)
	}
	if _, err := os.Stat(filepath.Join(data, "bin", BinName())); err != nil {
		t.Errorf("the download was not cached in %s: %v", data, err)
	}
}

func TestBootstrapPS1RestoresFromTheCacheAfterAPluginUpdate(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	cacheDir := filepath.Join(data, "bin")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, realBinary(t), filepath.Join(cacheDir, BinName()))

	out, code := runBootstrapPS(t, root, data, "http://127.0.0.1:1/unreachable")
	if code != 0 {
		t.Fatalf("bootstrap.ps1 exited %d\n%s", code, out)
	}
	mustRun(t, BinPath(root))
}

func TestBootstrapPS1ExitsZeroWhenTheDownloadFails(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, _ := serveRelease(t, []byte("404 not a tarball"))

	out, code := runBootstrapPS(t, root, data, srv.URL+"/wrong-prefix")
	if code != 0 {
		t.Fatalf("bootstrap.ps1 exited %d; a failed download must not break the session\n%s", code, out)
	}
	if _, err := os.Stat(BinPath(root)); err == nil {
		t.Error("a half-installed binary was left in the plugin root; the hooks would run it")
	}
	if !strings.Contains(out, "nothing is being gated") {
		t.Errorf("a failed install must say so in plain words; output was:\n%s", out)
	}
}

// hooks.json dispatches with `||`, which only works because bootstrap.sh exits 0 on a failed
// DOWNLOAD. If that ever changes, the Windows fallback starts firing on Unix.
func TestTheHookFallbackFiresOnlyWhenBashIsAbsent(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "plugin", "claude-code", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(string(b), "\n") {
		// `"command"`, not just the file name: the file's own description mentions it too.
		if !strings.Contains(line, "bootstrap.sh") || !strings.Contains(line, `"command"`) {
			continue
		}
		found = true
		if !strings.Contains(line, "||") || !strings.Contains(line, "bootstrap.ps1") {
			t.Errorf("the bootstrap hook does not fall back to PowerShell:\n  %s\n"+
				"Windows without Git Bash has no other way to install the binary.",
				strings.TrimSpace(line))
		}
	}
	if !found {
		t.Fatal("hooks.json never invokes bootstrap.sh")
	}
	// Both scripts end in `exit 0` for this reason. Guarded by
	// TestBootstrap{,PS1}ExitsZero...WhenTheDownloadFails above; this is the note that says why.
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// --- the other three files that spell the same contract ---

func TestManifestsInvokeTheBinaryBootstrapInstalls(t *testing.T) {
	pkg := filepath.Join(repoRoot(t), "plugin", "claude-code")

	var hf struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	readJSON(t, filepath.Join(pkg, "hooks", "hooks.json"), &hf)

	invocations := 0
	for event, groups := range hf.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				switch {
				case strings.Contains(h.Command, "bootstrap"):
					continue // checked by TestBootstrapHookRunsAScriptThatShips
				case !strings.Contains(h.Command, HookedBinRef):
					t.Errorf("%s hook runs %q; the only binary a release populates is %s",
						event, h.Command, HookedBinRef)
				default:
					invocations++
				}
				if strings.Contains(h.Command, "CLAUDE_PLUGIN_DATA") {
					t.Errorf("%s hook reads ${CLAUDE_PLUGIN_DATA}: that directory is a cache. "+
						"Claude Code puts ${CLAUDE_PLUGIN_ROOT}/bin on PATH, and the bare `batten` "+
						"in the /batten-* commands resolves through it", event)
				}
			}
		}
	}
	if invocations == 0 {
		t.Fatal("no hook invokes batten at all")
	}

	var mc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	readJSON(t, filepath.Join(pkg, ".mcp.json"), &mc)
	srv, ok := mc.MCPServers["batten"]
	if !ok {
		t.Fatal(".mcp.json declares no batten server")
	}
	if !strings.Contains(srv.Command, HookedBinRef) {
		t.Errorf(".mcp.json runs %q; the only binary a release populates is %s", srv.Command, HookedBinRef)
	}
}

// The bootstrap hook names a path inside the SHIPPED package, not inside this checkout.
func TestBootstrapHookRunsAScriptThatShips(t *testing.T) {
	pkg := filepath.Join(repoRoot(t), "plugin", "claude-code")
	b, err := os.ReadFile(filepath.Join(pkg, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bootstrap.sh", "bootstrap.ps1"} {
		if !strings.Contains(string(b), name) {
			t.Errorf("hooks.json never invokes %s, so nothing installs the binary on that platform", name)
			continue
		}
		if _, err := os.Stat(filepath.Join(pkg, "scripts", name)); err != nil {
			t.Errorf("hooks.json invokes %s but the package does not ship it: %v", name, err)
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
