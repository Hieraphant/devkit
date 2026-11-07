//go:build !windows

package runners

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAgy puts an `agy` shell script on PATH and clears the AGY_* overrides
// so Run exercises the default argv.
func fakeAgy(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGY_CMD", "")
	t.Setenv("AGY_ARGS", "")
}

// echoScript reports argv, the devkit_ask depth guard, cwd and stdin so a
// test can assert the whole process contract in one run.
const echoScript = `printf 'args=%s\n' "$*"
printf 'depth=%s\n' "${DEVKIT_ASK_DEPTH:-unset}"
printf 'cwd=%s\n' "$(pwd)"
printf 'stdin=%s\n' "$(cat)"
exit 0
`

func TestAgyRun_PipesPromptAppliesEnvAndWorkDir(t *testing.T) {
	fakeAgy(t, echoScript)
	workDir := t.TempDir()

	res, err := (&AgyRunner{}).Run(context.Background(), "hello agy", RunOpts{
		WorkDir: workDir,
		Env:     []string{"DEVKIT_ASK_DEPTH=1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output %q)", err, res.Output)
	}
	for _, want := range []string{
		"args=--dangerously-skip-permissions --print\n",
		"depth=1\n",
		"stdin=hello agy\n",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output missing %q:\n%s", want, res.Output)
		}
	}
	// TempDir may sit behind a symlink (macOS /private); compare resolved paths.
	wantCwd, _ := filepath.EvalSymlinks(workDir)
	if !strings.Contains(res.Output, "cwd="+wantCwd+"\n") {
		t.Errorf("output should report cwd=%s:\n%s", wantCwd, res.Output)
	}
}

func TestAgyRun_InheritsEnvWhenNoneGiven(t *testing.T) {
	fakeAgy(t, echoScript)
	t.Setenv("DEVKIT_ASK_DEPTH", "")

	res, err := (&AgyRunner{}).Run(context.Background(), "hi", RunOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Output, "depth=unset\n") {
		t.Errorf("no Env given, so the child must not see a depth:\n%s", res.Output)
	}
}

func TestAgyRun_ExitZeroErrorLineIsFailure(t *testing.T) {
	fakeAgy(t, "cat >/dev/null\nprintf 'Error: timed out waiting for response\\n'\nexit 0\n")

	res, err := (&AgyRunner{}).Run(context.Background(), "hi", RunOpts{})
	if err == nil {
		t.Fatalf("expected error for exit-0 Error: line, got nil (output %q)", res.Output)
	}
	if !strings.Contains(err.Error(), "agy reported error") || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should carry the agy message, got %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
}

func TestAgyRun_NonZeroExitCarriesStderr(t *testing.T) {
	fakeAgy(t, "cat >/dev/null\nprintf 'partial answer'\nprintf 'AGY_ERROR: {\"short_error\":\"print timeout\"}\\n' >&2\nexit 3\n")

	res, err := (&AgyRunner{}).Run(context.Background(), "hi", RunOpts{})
	if err == nil {
		t.Fatal("expected error for exit 3, got nil")
	}
	if !strings.Contains(err.Error(), "agy exited 3") || !strings.Contains(err.Error(), "AGY_ERROR") {
		t.Errorf("error should carry exit code and stderr, got %v", err)
	}
	if res.ExitCode != 3 || res.Output != "partial answer" {
		t.Errorf("result = %+v, want exit 3 with partial output preserved", res)
	}
}
