package tools

import "testing"

func TestReadOnlyGitCommand(t *testing.T) {
	for _, command := range []string{"status", "log", "show", "blame", "diff-tree", "ls-tree", "rev-list"} {
		if !readOnlyGitCommand(command) {
			t.Fatalf("%q should be permitted", command)
		}
	}
	for _, command := range []string{"pull", "push", "commit", "reset", "checkout", "switch", "clean", "config"} {
		if readOnlyGitCommand(command) {
			t.Fatalf("%q must not be permitted", command)
		}
	}
}

func TestHasUnsafeGitArg(t *testing.T) {
	for _, args := range [][]string{{"--output=result.patch"}, {"--ext-diff"}, {"--no-index"}} {
		if !hasUnsafeGitArg(args) {
			t.Fatalf("%v should be rejected", args)
		}
	}
	if hasUnsafeGitArg([]string{"--oneline", "HEAD~1..HEAD"}) {
		t.Fatal("ordinary query arguments should be accepted")
	}
}
