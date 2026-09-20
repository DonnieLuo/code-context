package tools

import (
	"testing"

	"github.com/example/code-context/internal/lsp"
)

func TestLocationCarriesSymbolMetadata(t *testing.T) {
	location := Location{Snippet: "createOrder", SymbolKind: 6, SymbolType: symbolKindName(6), Container: "OrderService"}
	if location.SymbolKind != 6 || location.SymbolType != "method" || location.Container != "OrderService" {
		t.Fatalf("unexpected symbol location: %#v", location)
	}
	// Keep this compile-time field check close to the conversion contract.
	_ = lsp.Symbol{Kind: location.SymbolKind, ContainerName: location.Container}
}

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
