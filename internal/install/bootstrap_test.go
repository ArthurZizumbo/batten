package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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

// serveRelease stands in for releases/latest/download: the six assets plus the checksums.txt
// GoReleaser publishes beside them. It records what was asked for.
func serveRelease(t *testing.T, tgz []byte) (*httptest.Server, *string) {
	t.Helper()
	sums := checksumsFor(tgz)
	srv, asked, _ := serveReleaseFiles(t, tgz, &sums)
	return srv, asked
}

// serveReleaseFiles is serveRelease with the checksums.txt under test. A nil body 404s it, which
// is the mirror-incomplete case: a BATTEN_BOOTSTRAP_BASE_URL pointed at somewhere that serves the
// archive and not the sums.
//
// The second return value records every path requested, so a test can assert that bootstrap
// actually FETCHED the checksums rather than merely surviving their presence.
func serveReleaseFiles(t *testing.T, tgz []byte, sums *string) (*httptest.Server, *string, *[]string) {
	t.Helper()
	var asked string
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			if sums == nil {
				http.Error(w, "no checksums here", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(*sums))
			return
		}
		asked = r.URL.Path
		if !assetName.MatchString(r.URL.Path) {
			http.Error(w, "no such asset", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tgz)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked, &paths
}

// checksumsFor writes the file GoReleaser's `checksum:` block produces: one `<sha256>  <name>`
// line per published asset. All six names are listed and only one of them is ever on disk, which
// is precisely why bootstrap cannot use a bare `sha256sum -c`.
func checksumsFor(body []byte) string {
	sum := hex.EncodeToString(sha256Sum(body))
	var b strings.Builder
	for _, os := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			fmt.Fprintf(&b, "%s  batten_%s_%s.tar.gz\n", sum, os, arch)
		}
	}
	return b.String()
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// requested reports whether the server was asked for a path ending in suffix.
func requested(paths *[]string, suffix string) bool {
	for _, p := range *paths {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
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

// --- the tampering matrix (plan §3.1) ---
//
// bootstrap downloaded 14 MB and verified nothing. The only post-download check was that the
// binary answers `version`, which a hostile binary answers happily — and seven hooks plus an MCP
// server execute that file. `checksums.txt` was already published by every release
// (.goreleaser.yaml `checksum:`) for nobody to read.
//
// These four are the whole contract, and each one is a different way for "nobody can vouch for
// these bytes" to be true. They all get the same answer: refuse, change nothing, exit 0.

// refusedAndLeftNothing asserts the fail-closed contract in the three places it has to hold.
func refusedAndLeftNothing(t *testing.T, root, data, out string, code int) {
	t.Helper()
	// The exit code is a different sentence. hooks.json dispatches
	// `bash bootstrap.sh || powershell bootstrap.ps1`, so a non-zero exit here means "there is
	// no bash" and would fire the Windows fallback on macOS.
	if code != 0 {
		t.Errorf("bootstrap exited %d; that exit code means \"no bash\", not \"bad download\"\n%s", code, out)
	}
	if _, err := os.Stat(BinPath(root)); err == nil {
		t.Error("an unverified binary was installed where seven hooks and the MCP server run it")
	}
	if _, err := os.Stat(filepath.Join(data, "bin", BinName())); err == nil {
		t.Error("an unverified binary reached the cache. The cache restores WITHOUT network and " +
			"therefore without a second chance to check: one bad download would outlive itself")
	}
	if !strings.Contains(out, "nothing is being gated") {
		t.Errorf("a refused install must say so in plain words; output was:\n%s", out)
	}
}

// Hash A published, bytes B served: the shape of an asset-replacement attack and of a plain
// corrupt download, which are indistinguishable from here and deserve the same answer.
func TestBootstrapRefusesATamperedArchive(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	published := []byte("the archive this release actually published")
	sums := checksumsFor(published)
	srv, _, paths := serveReleaseFiles(t, releaseArchive(t), &sums)

	out, code := runBootstrap(t, root, data, srv.URL)
	refusedAndLeftNothing(t, root, data, out, code)

	if !requested(paths, "/checksums.txt") {
		t.Error("bootstrap never asked for checksums.txt at all")
	}
	// stderr has to name all three, because the person reading it has to be able to tell a
	// tampered asset from a stale mirror without running anything themselves.
	wantHash := hex.EncodeToString(sha256Sum(published))
	gotHash := hex.EncodeToString(sha256Sum(releaseArchive(t)))
	for _, want := range []string{"MISMATCH", srv.URL, wantHash, gotHash} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must name the url, the expected hash and the one it got. Missing %q from:\n%s", want, out)
		}
	}
}

// The camino feliz is still the camino feliz — and it verified. Asserting that checksums.txt was
// FETCHED is what makes this differential: a bootstrap that ignores the file installs just as
// happily, and every other assertion here would still pass.
func TestBootstrapInstallsWhenChecksumMatches(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	sums := checksumsFor(releaseArchive(t))
	srv, asked, paths := serveReleaseFiles(t, releaseArchive(t), &sums)

	out, code := runBootstrap(t, root, data, srv.URL)
	if code != 0 {
		t.Fatalf("bootstrap exited %d\n%s", code, out)
	}
	mustRun(t, BinPath(root))
	if !assetName.MatchString(*asked) {
		t.Errorf("bootstrap asked for %q, which is not a name .goreleaser.yaml builds", *asked)
	}
	if !requested(paths, "/checksums.txt") {
		t.Error("the install succeeded without ever fetching checksums.txt: the verification is " +
			"beside the install path, not in it")
	}
	if _, err := os.Stat(filepath.Join(data, "bin", BinName())); err != nil {
		t.Errorf("verified bytes were not cached, so the next plugin update pays 14 MB again: %v", err)
	}
}

// checksums.txt unreachable gets the SAME treatment as a mismatch, and the reason is written in
// plan §3.1: for this project's own releases a 404 is counterfactual (GoReleaser has produced the
// file since before the first tag), so the reachable case is a BATTEN_BOOTSTRAP_BASE_URL aimed at
// an incomplete mirror — where "install it anyway" is precisely the wrong answer.
func TestBootstrapFailsClosedWithoutChecksums(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, _, paths := serveReleaseFiles(t, releaseArchive(t), nil) // 404 on checksums.txt

	out, code := runBootstrap(t, root, data, srv.URL)
	refusedAndLeftNothing(t, root, data, out, code)

	if !requested(paths, "/checksums.txt") {
		t.Error("bootstrap never asked for checksums.txt at all")
	}
	if !strings.Contains(out, "checksums.txt") {
		t.Errorf("the refusal must name the file it could not get:\n%s", out)
	}
}

// The claim §3.2 rests its whole argument on: the ungated window is the FIRST install only.
//
// Act 1 is the differential half — a tampered first download must not reach the cache. Act 2 is
// the payoff: with the cache holding what a verified install left, a plugin update restores it
// without network while the server is still serving the bad archive. Act 3 is the control.
func TestCacheRestoreSurvivesABadDownload(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	sums := checksumsFor([]byte("a different release"))
	srv, _, _ := serveReleaseFiles(t, releaseArchive(t), &sums)

	out, code := runBootstrap(t, root, data, srv.URL)
	refusedAndLeftNothing(t, root, data, out, code)

	// Act 2. The cache is what a verified install leaves behind; ${CLAUDE_PLUGIN_ROOT}/bin ships
	// empty, so an update has wiped the binary. The download is still poisoned.
	cacheDir := filepath.Join(data, "bin")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, realBinary(t), filepath.Join(cacheDir, BinName()))

	out, code = runBootstrap(t, root, data, srv.URL)
	if code != 0 {
		t.Fatalf("bootstrap exited %d\n%s", code, out)
	}
	mustRun(t, BinPath(root))

	gateDenies(t, BinPath(root))
}

// gateDenies proves the restored binary is GOVERNING, not merely executing.
//
// Every other assertion in this file is satisfied by anything that answers `version`, and a hook
// that prints nothing is exactly what an ALLOW looks like — so "it runs" and "the gate is back"
// are different claims and only one of them is being made here. The control is positive: a commit
// that must be denied, denied in words.
func gateDenies(t *testing.T, bin string) {
	t.Helper()
	repo := t.TempDir()
	spec := "version: 1\nproject: p\nenforcement: enforce\n" +
		"unit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n"
	if err := os.WriteFile(filepath.Join(repo, "batten.yaml"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	// A database in this test's own temp directory. The author's ~/.batten/batten.db is one
	// unset variable away, and a test that writes real runs into it is a test that lies.
	db := filepath.Join(t.TempDir(), "gate.db")
	env := append(os.Environ(), "BATTEN_DB="+db)

	open := exec.Command(bin, "phase", "TASK-001", "build")
	open.Dir, open.Env = repo, env
	if b, err := open.CombinedOutput(); err != nil {
		t.Fatalf("could not open a run with the restored binary: %v\n%s", err, b)
	}

	payload, err := json.Marshal(map[string]any{
		"session_id":      "s",
		"cwd":             repo,
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": `git commit -m "TASK-001 x"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(bin, "hook", "PreToolUse")
	hook.Dir, hook.Env, hook.Stdin = repo, env, bytes.NewReader(payload)
	b, err := hook.CombinedOutput()
	if err != nil {
		t.Fatalf("hook PreToolUse: %v\n%s", err, b)
	}
	if !strings.Contains(string(b), `"permissionDecision":"deny"`) {
		t.Errorf("the restored binary answers `version`, but a commit with no verdict was NOT "+
			"denied. Silence from this hook is indistinguishable from approval, so a passing "+
			"`version` proves nothing about the gate:\n%s", b)
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
	sums := checksumsFor(releaseArchive(t))
	srv, asked, paths := serveReleaseFiles(t, releaseArchive(t), &sums)

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
	if !requested(paths, "/checksums.txt") {
		t.Error("bootstrap.ps1 installed without fetching checksums.txt; the two scripts are one " +
			"contract in two files and this is the half a Windows user runs")
	}
	if _, err := os.Stat(filepath.Join(data, "bin", BinName())); err != nil {
		t.Errorf("the download was not cached in %s: %v", data, err)
	}
}

// The tampering matrix, on the script Windows actually runs. Same two failures, same refusal —
// "green in BOTH scripts" is the acceptance criterion, and Windows is the declared primary target.
func TestBootstrapPS1RefusesATamperedArchive(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	published := []byte("the archive this release actually published")
	sums := checksumsFor(published)
	srv, _, paths := serveReleaseFiles(t, releaseArchive(t), &sums)

	out, code := runBootstrapPS(t, root, data, srv.URL)
	refusedAndLeftNothing(t, root, data, out, code)

	if !requested(paths, "/checksums.txt") {
		t.Error("bootstrap.ps1 never asked for checksums.txt at all")
	}
	wantHash := hex.EncodeToString(sha256Sum(published))
	gotHash := hex.EncodeToString(sha256Sum(releaseArchive(t)))
	for _, want := range []string{"MISMATCH", srv.URL, wantHash, gotHash} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must name the url, the expected hash and the one it got. Missing %q from:\n%s", want, out)
		}
	}
}

func TestBootstrapPS1FailsClosedWithoutChecksums(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	srv, _, paths := serveReleaseFiles(t, releaseArchive(t), nil) // 404 on checksums.txt

	out, code := runBootstrapPS(t, root, data, srv.URL)
	refusedAndLeftNothing(t, root, data, out, code)

	if !requested(paths, "/checksums.txt") {
		t.Error("bootstrap.ps1 never asked for checksums.txt at all")
	}
	if !strings.Contains(out, "checksums.txt") {
		t.Errorf("the refusal must name the file it could not get:\n%s", out)
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
