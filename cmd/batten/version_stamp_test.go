package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The release stamps the version through the linker: `.goreleaser.yaml` passes
// -X main.version={{.Version}}. That flag is silently ignored unless `version` is declared
// uninitialized or initialized to a constant — cmd/link says so in as many words:
//
//	"This is only effective if the variable is declared in the source code either uninitialized
//	 or initialized to a constant string expression. -X will not work if the initializer makes
//	 a function call..."
//
// It was `var version = buildVersion("0.1.0")`, a function call, so the flag did nothing and had
// never done anything. Nothing failed: no error from the linker, no warning from the build, and
// `batten version` printed a plausible-looking string the whole time. That is why this test runs
// the linker instead of reading the source — the defect is invisible to anything that does less.
func TestTheLinkerCanStampTheVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a binary")
	}

	bin := filepath.Join(t.TempDir(), "batten")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	const sentinel = "9.9.9-stamped-by-the-linker"
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+sentinel, "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	if got := strings.TrimSpace(string(out)); got != "batten "+sentinel {
		t.Errorf("the linker could not stamp the version: `batten version` printed %q, want %q.\n"+
			"That means -X main.version is being ignored, so every release reports something "+
			"other than its tag — and `batten doctor` compares this string against plugin.json",
			got, "batten "+sentinel)
	}
}

// The other half of the contract: an unstamped build must still answer "which batten is this"
// rather than printing an empty version. This is the `go install` and plain-checkout path, and it
// is the one people are told to use when the antivirus false positive hits them.
func TestAnUnstampedBuildStillReportsAVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a binary")
	}

	bin := filepath.Join(t.TempDir(), "batten")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	got := strings.TrimSpace(string(out))
	if got == "batten" || got == "batten " {
		t.Errorf("an unstamped build printed %q — the fallback did not run, so anyone who built "+
			"batten themselves gets a blank version and `doctor` tells them to reinstall", got)
	}
}
