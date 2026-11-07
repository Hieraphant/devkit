package runners

import (
	"context"
	"os"
)

type RunResult struct {
	Output    string
	SessionID string
	TokensIn  int
	TokensOut int
	CostUSD   float64
	ExitCode  int
}

type RunOpts struct {
	WorkDir                string
	AllowedTools           string
	AppendSystemPromptFile string
	AppendSystemPrompt     string
	MaxTurns               int
	// Env holds extra KEY=VALUE pairs appended to the inherited environment
	// of a spawned CLI. devkit_ask uses it to pass DEVKIT_ASK_DEPTH down the
	// process tree so a peer it launched cannot launch another.
	Env []string
}

type Runner interface {
	Name() string
	Available() bool
	Run(ctx context.Context, prompt string, opts RunOpts) (RunResult, error)
}

func DetectRunners() []Runner {
	all := []Runner{
		&ClaudeRunner{},
		&CodexRunner{},
		&GeminiRunner{},
		&AgyRunner{},
		&LocalRunner{},
	}
	var available []Runner
	for _, r := range all {
		if r.Available() {
			available = append(available, r)
		}
	}
	return available
}

func FindRunner(name string, runners []Runner) Runner {
	for _, r := range runners {
		if r.Name() == name {
			return r
		}
	}
	return nil
}

// childEnv returns the environment for a spawned CLI: nil (inherit the
// parent environment unchanged) when opts.Env is empty, otherwise the
// parent environment with opts.Env appended. exec.Cmd keeps the last value
// for a duplicated key, so entries in opts.Env override inherited ones.
func childEnv(opts RunOpts) []string {
	if len(opts.Env) == 0 {
		return nil
	}
	return append(os.Environ(), opts.Env...)
}

// TruncStr truncates a string to n runes with "..." suffix.
func TruncStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
