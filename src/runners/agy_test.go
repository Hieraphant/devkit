package runners

import "testing"

func TestAgyRunnerName(t *testing.T) {
	r := &AgyRunner{}
	if r.Name() != "agy" {
		t.Errorf("got %q, want %q", r.Name(), "agy")
	}
}

func TestAgyExecArgs(t *testing.T) {
	tests := []struct {
		name    string
		agyArgs string
		workDir string
		want    []string
	}{
		{
			// --print must stay last: a valueless --print followed by
			// another flag swallowed that flag as the prompt on some
			// releases (fixed in v1.5.2 release notes).
			name: "default no workdir",
			want: []string{"--dangerously-skip-permissions", "--print"},
		},
		{
			// agy has no cwd flag; WorkDir is applied via cmd.Dir only.
			name:    "default with workdir",
			workDir: "/tmp/repo",
			want:    []string{"--dangerously-skip-permissions", "--print"},
		},
		{
			name:    "AGY_ARGS override with workdir token",
			agyArgs: "--add-dir {workdir} --print",
			workDir: "/tmp/repo",
			want:    []string{"--add-dir", "/tmp/repo", "--print"},
		},
		{
			name:    "AGY_ARGS override expands workdir token to . when unset",
			agyArgs: "--add-dir {workdir} --print",
			want:    []string{"--add-dir", ".", "--print"},
		},
		{
			name:    "AGY_ARGS override without token",
			agyArgs: "  --dangerously-skip-permissions --sandbox --print  ",
			want:    []string{"--dangerously-skip-permissions", "--sandbox", "--print"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv with "" forces the default branch (TrimSpace == "").
			t.Setenv("AGY_ARGS", tt.agyArgs)
			got := agyExecArgs(tt.workDir)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("arg[%d] = %q, want %q (all args: %v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestAgyCmdOverride(t *testing.T) {
	t.Setenv("AGY_CMD", "my-custom-agy")
	if got := agyCmd(); got != "my-custom-agy" {
		t.Errorf("agyCmd() = %q, want %q", got, "my-custom-agy")
	}
	// Empty override falls back to the default binary name.
	t.Setenv("AGY_CMD", "")
	if got := agyCmd(); got != "agy" {
		t.Errorf("agyCmd() default = %q, want %q", got, "agy")
	}
}

func TestAgyAvailableFalseWhenBinaryMissing(t *testing.T) {
	t.Setenv("AGY_CMD", "definitely-not-a-real-binary-xyz123")
	r := &AgyRunner{}
	if r.Available() {
		t.Error("Available() = true for a nonexistent binary, want false")
	}
}

func TestAgyOutputIsError(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"observed print-timeout", "Error: timed out waiting for response", true},
		{"hypothetical print timeout", "Error: print timeout", true},
		{"auth failure", "Error: authentication failed: no credentials", true},
		{"leading and trailing whitespace", "  Error: rate limited\n", true},

		{"model discussing errors (multi-paragraph)", "Error: NullPointerException at line 42\n\nThe root cause is the nil dereference in handleRequest()...", false},
		{"long model response starting with Error:", "Error: " + longString(400), false},
		{"Error: mid-body, not at prefix", "Here's a review.\n\nError: this is a discussion of error handling.", false},
		{"empty output", "", false},
		{"whitespace only", "   \n  \t  ", false},
		{"lowercase error: is not an agy error", "error: lowercase from a model response", false},
		{"uppercase ERROR: is not matched (case-sensitive)", "ERROR: yelling", false},
		{"Error- with dash, not colon", "Error- not the right delimiter", false},
		{"plain success output", "ok", false},
		{"multi-line model output", "Here is my review:\n- point 1\n- point 2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agyOutputIsError(tt.output); got != tt.want {
				t.Errorf("agyOutputIsError(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
