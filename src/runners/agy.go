package runners

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// AgyRunner dispatches to the Google Antigravity CLI ("agy") for a single
// headless request/response. The prompt is piped via stdin, stdout is
// returned verbatim, and failures are surfaced with truncated stderr.
//
// Default invocation: `agy --dangerously-skip-permissions --print`, prompt on
// stdin, working directory via cmd.Dir. Evidence for each piece:
//
//   - `--print` (alias -p/--prompt) runs one prompt non-interactively and,
//     when given no value, reads the prompt from stdin. 5uck1ess/devkit#102
//     smoke-tested exactly this stdin shape against agy v1.0.0; the headless
//     reference (https://antigravity.google/docs/cli/headless/) documents
//     the flag. `--print` goes LAST: the v1.5.2 release notes record a fix
//     for "a valueless prompt flag swallowing the next flag as its prompt",
//     and trailing `--print` is immune to that bug on every version.
//   - `--dangerously-skip-permissions` auto-approves tool permission
//     requests — the agy analogue of `gemini -y`, which GeminiRunner ships.
//     Headless agy cannot prompt: without the flag any tool needing approval
//     is soft-denied with a stderr notice and the run still exits 0, so the
//     "answer" would silently be produced without the tools it needed.
//   - agy has no working-directory flag; headless runs report the process
//     cwd, so WorkDir is applied through cmd.Dir only.
//   - Output is plain text by default (no --output-format needed).
//   - `--sandbox` (terminal sandbox) is documented and is honoured in print
//     mode since v1.5.x, but it is NOT in the default set: the CLI reports
//     "terminal sandbox unavailable" on hosts without sandbox support and
//     the combination has not been exercised against a real install. Opt in
//     with AGY_ARGS once verified on your host.
//
// Two environment overrides exist so an operator can adapt to a newer agy
// without recompiling:
//
//	AGY_CMD   — binary name/path (default "agy"); used by Available() and Run().
//	AGY_ARGS  — space-separated argv that REPLACES the default flag set, e.g.
//	            AGY_ARGS="--dangerously-skip-permissions --sandbox --print".
//	            The literal token {workdir} expands to RunOpts.WorkDir, or to
//	            "." when no WorkDir is set (cmd.Dir is unset then, so "." is
//	            the same directory), e.g. "--add-dir {workdir} --print".
type AgyRunner struct{}

func (r *AgyRunner) Name() string { return "agy" }

// agyCmd resolves the agy binary name (AGY_CMD override, default "agy").
func agyCmd() string { return envDefault("AGY_CMD", "agy") }

func (r *AgyRunner) Available() bool {
	_, err := exec.LookPath(agyCmd())
	return err == nil
}

// agyExecArgs builds the argv for a one-shot, stdin-fed request. See the
// AgyRunner doc comment for why the default is shaped this way.
func agyExecArgs(workDir string) []string {
	if raw := strings.TrimSpace(os.Getenv("AGY_ARGS")); raw != "" {
		if workDir == "" {
			workDir = "."
		}
		fields := strings.Fields(raw)
		args := make([]string, 0, len(fields))
		for _, f := range fields {
			if f == "{workdir}" {
				f = workDir
			}
			args = append(args, f)
		}
		return args
	}
	return []string{"--dangerously-skip-permissions", "--print"}
}

func (r *AgyRunner) Run(ctx context.Context, prompt string, opts RunOpts) (RunResult, error) {
	// agy --print reads the prompt from stdin; piping avoids arg-length
	// limits on large prompts/diffs, matching codex/gemini.
	cmd := exec.CommandContext(ctx, agyCmd(), agyExecArgs(opts.WorkDir)...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Env = childEnv(opts)
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return RunResult{ExitCode: 1}, fmt.Errorf("agy failed to run: %w", err)
		}
	}

	output := stdout.String()
	result := RunResult{Output: output, ExitCode: exitCode}
	if exitCode != 0 {
		// Current releases exit 3 on print-timeout / model failures and
		// write an `AGY_ERROR: {...}` line to stderr, which lands here.
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = output
		}
		return result, fmt.Errorf("agy exited %d: %s", exitCode, TruncStr(errMsg, 200))
	}
	// Observed against agy v1.0.0 (5uck1ess/devkit#102): print-timeout and
	// similar internal failures exited 0 with a short single-line
	// "Error: ..." on stdout. Treat that as a failed run so a caller never
	// consumes an error string as a model answer; see agyOutputIsError.
	if agyOutputIsError(output) {
		return result, fmt.Errorf("agy reported error: %s", TruncStr(strings.TrimSpace(output), 200))
	}
	return result, nil
}

// agyOutputIsError decides whether an exit-0 stdout payload from `agy --print`
// is an agy-emitted error rather than a model response.
//
// Genuine agy errors are short, single-line, case-sensitive "Error: ..."
// strings. Model output that legitimately discusses errors is typically
// multi-paragraph (contains a blank line) or substantially longer, so it
// passes through.
func agyOutputIsError(output string) bool {
	trimmed := strings.TrimSpace(output)
	if !strings.HasPrefix(trimmed, "Error:") {
		return false
	}
	if len(trimmed) > 300 {
		return false
	}
	if strings.Contains(trimmed, "\n\n") {
		return false
	}
	return true
}
