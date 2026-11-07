package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/5uck1ess/devkit/runners"
	mcpmcp "github.com/mark3labs/mcp-go/mcp"
)

// fakePeer is a Runner stub that records what devkit_ask hands it.
type fakePeer struct {
	name string
	out  string
	code int
	err  error
	// block makes Run wait for ctx cancellation and return ctx.Err().
	block bool

	called    bool
	gotPrompt string
	gotOpts   runners.RunOpts
	deadline  time.Time
}

func (f *fakePeer) Name() string    { return f.name }
func (f *fakePeer) Available() bool { return true }
func (f *fakePeer) Run(ctx context.Context, prompt string, opts runners.RunOpts) (runners.RunResult, error) {
	f.called = true
	f.gotPrompt = prompt
	f.gotOpts = opts
	f.deadline, _ = ctx.Deadline()
	if f.block {
		<-ctx.Done()
		return runners.RunResult{ExitCode: -1}, ctx.Err()
	}
	return runners.RunResult{Output: f.out, ExitCode: f.code}, f.err
}

// withFakePeers replaces peer discovery for the duration of the test and
// clears DEVKIT_ASK_DEPTH so the host environment cannot trip the guard.
func withFakePeers(t *testing.T, peers ...runners.Runner) {
	t.Helper()
	t.Setenv(askDepthEnv, "")
	prev := askDetectRunners
	askDetectRunners = func() []runners.Runner { return peers }
	t.Cleanup(func() { askDetectRunners = prev })
}

func askHandler(t *testing.T) func(context.Context, mcpmcp.CallToolRequest) (*mcpmcp.CallToolResult, error) {
	t.Helper()
	srv := newTestServer(t, t.TempDir(), t.TempDir())
	_, handler := srv.askTool()
	return handler
}

func TestAskMissingArgs(t *testing.T) {
	withFakePeers(t)
	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{})
	if !isErr {
		t.Fatalf("expected IsError=true for missing args, got: %s", out)
	}
	if !strings.Contains(out, "missing argument") {
		t.Errorf("expected 'missing argument' in output, got: %s", out)
	}
}

func TestAskUnknownPeer(t *testing.T) {
	withFakePeers(t, &fakePeer{name: "gemini", out: "nope"})
	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":     "gemini",
		"prompt": "hi",
	})
	if !isErr {
		t.Fatalf("expected IsError=true for unknown peer, got: %s", out)
	}
	if !strings.Contains(out, "unknown peer") {
		t.Errorf("expected 'unknown peer' in output, got: %s", out)
	}
}

func TestAskPeerUnavailable(t *testing.T) {
	// No peers discovered at all — deterministic regardless of what the
	// host has on PATH.
	withFakePeers(t)
	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":     "agy",
		"prompt": "hi",
	})
	if !isErr {
		t.Fatalf("expected IsError=true for unavailable peer, got: %s", out)
	}
	if !strings.Contains(out, "not available") {
		t.Errorf("expected 'not available' in output, got: %s", out)
	}
}

func TestAskForwardsPromptWorkdirAndDepth(t *testing.T) {
	peer := &fakePeer{name: "codex", out: "answer"}
	withFakePeers(t, peer)
	dir := t.TempDir()

	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":      "Codex",
		"prompt":  "what is 2+2",
		"workdir": dir,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if out != "answer" {
		t.Errorf("output = %q, want %q", out, "answer")
	}
	if peer.gotPrompt != "what is 2+2" {
		t.Errorf("prompt = %q", peer.gotPrompt)
	}
	if peer.gotOpts.WorkDir != dir {
		t.Errorf("WorkDir = %q, want %q", peer.gotOpts.WorkDir, dir)
	}
	want := askDepthEnv + "=1"
	found := false
	for _, kv := range peer.gotOpts.Env {
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Errorf("Env = %v, want it to contain %q", peer.gotOpts.Env, want)
	}
	remaining := time.Until(peer.deadline)
	if remaining <= commandTimeout-10*time.Second || remaining > commandTimeout {
		t.Errorf("default deadline %s from now, want ≈ %s", remaining, commandTimeout)
	}
}

func TestAskRefusesNestedCall(t *testing.T) {
	peer := &fakePeer{name: "claude", out: "should not run"}
	withFakePeers(t, peer)
	t.Setenv(askDepthEnv, "1")

	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":     "claude",
		"prompt": "hi",
	})
	if !isErr {
		t.Fatalf("expected IsError=true for nested call, got: %s", out)
	}
	if !strings.Contains(out, "nested devkit_ask refused") {
		t.Errorf("expected nesting refusal, got: %s", out)
	}
	if peer.called {
		t.Error("peer must not run when the depth guard trips")
	}
}

func TestAskRejectsMalformedDepth(t *testing.T) {
	peer := &fakePeer{name: "claude"}
	withFakePeers(t, peer)
	t.Setenv(askDepthEnv, "banana")

	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":     "claude",
		"prompt": "hi",
	})
	if !isErr || !strings.Contains(out, "invalid "+askDepthEnv) {
		t.Fatalf("expected malformed-depth error, got isErr=%v: %s", isErr, out)
	}
	if peer.called {
		t.Error("peer must not run with a malformed depth guard")
	}
}

func TestAskRejectsBadWorkdir(t *testing.T) {
	peer := &fakePeer{name: "codex"}
	withFakePeers(t, peer)
	base := t.TempDir()
	file := filepath.Join(base, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, workdir, want string
	}{
		{"missing", filepath.Join(base, "missing"), "workdir"},
		{"file", file, "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
				"to":      "codex",
				"prompt":  "hi",
				"workdir": tc.workdir,
			})
			if !isErr || !strings.Contains(out, tc.want) {
				t.Fatalf("expected error containing %q, got isErr=%v: %s", tc.want, isErr, out)
			}
		})
	}
	if peer.called {
		t.Error("peer must not run with an invalid workdir")
	}
}

func TestAskCustomTimeout(t *testing.T) {
	peer := &fakePeer{name: "codex", out: "ok"}
	withFakePeers(t, peer)

	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":      "codex",
		"prompt":  "hi",
		"timeout": 42,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	remaining := time.Until(peer.deadline)
	if remaining <= 32*time.Second || remaining > 42*time.Second {
		t.Errorf("deadline %s from now, want ≈ 42s", remaining)
	}
}

func TestAskRejectsBadTimeout(t *testing.T) {
	peer := &fakePeer{name: "codex"}
	withFakePeers(t, peer)

	cases := []struct {
		name  string
		value interface{}
		want  string
	}{
		{"zero", 0, "invalid timeout"},
		{"negative", -5, "invalid timeout"},
		{"over cap", askMaxTimeout.Seconds() + 1, "exceeds maximum"},
		{"not a number", "soon", "invalid timeout"},
		{"nan", "NaN", "invalid timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
				"to":      "codex",
				"prompt":  "hi",
				"timeout": tc.value,
			})
			if !isErr || !strings.Contains(out, tc.want) {
				t.Fatalf("expected error containing %q, got isErr=%v: %s", tc.want, isErr, out)
			}
		})
	}
	if peer.called {
		t.Error("peer must not run with an invalid timeout")
	}
}

func TestAskSurfacesPeerFailure(t *testing.T) {
	withFakePeers(t, &fakePeer{name: "agy", out: "partial output", code: 3, err: errors.New("agy exited 3: boom")})

	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":     "agy",
		"prompt": "hi",
	})
	if !isErr {
		t.Fatalf("expected IsError=true, got: %s", out)
	}
	for _, want := range []string{"agy exited 3", "partial output", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestAskReportsTimeout(t *testing.T) {
	withFakePeers(t, &fakePeer{name: "claude", block: true})

	out, isErr := callToolHandler(t, askHandler(t), map[string]interface{}{
		"to":      "claude",
		"prompt":  "hi",
		"timeout": 0.05,
	})
	if !isErr {
		t.Fatalf("expected IsError=true, got: %s", out)
	}
	if !strings.Contains(out, "claude timed out after 50ms") {
		t.Errorf("expected timeout message, got: %s", out)
	}
}
