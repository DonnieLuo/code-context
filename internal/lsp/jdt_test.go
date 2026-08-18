package lsp

import (
	"context"
	"testing"
)

func TestTraverseCallHierarchyRespectsDepthAndDeduplicatesCycles(t *testing.T) {
	a := callItem("A.java", 1)
	b := callItem("B.java", 2)
	c := callItem("C.java", 3)
	edges := map[string][]CallHierarchyItem{
		callHierarchyKey(a): {b},
		callHierarchyKey(b): {c},
		callHierarchyKey(c): {a},
	}
	got, truncated, err := traverseCallHierarchy(context.Background(), []CallHierarchyItem{a}, 10, 10, func(item CallHierarchyItem) ([]CallHierarchyItem, error) {
		return edges[callHierarchyKey(item)], nil
	})
	if err != nil || truncated {
		t.Fatalf("traverse error=%v truncated=%v", err, truncated)
	}
	if len(got) != 2 || got[0].Name != "B.java" || got[0].Depth != 1 || got[1].Name != "C.java" || got[1].Depth != 2 {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestTraverseCallHierarchyMarksResultLimit(t *testing.T) {
	a := callItem("A.java", 1)
	b := callItem("B.java", 2)
	c := callItem("C.java", 3)
	got, truncated, err := traverseCallHierarchy(context.Background(), []CallHierarchyItem{a}, 1, 1, func(item CallHierarchyItem) ([]CallHierarchyItem, error) {
		return []CallHierarchyItem{b, c}, nil
	})
	if err != nil || !truncated || len(got) != 1 {
		t.Fatalf("got=%#v truncated=%v err=%v", got, truncated, err)
	}
}

func callItem(uri string, line int) CallHierarchyItem {
	return CallHierarchyItem{Name: uri, URI: "file:///" + uri, SelectionRange: Range{Start: Position{Line: line}, End: Position{Line: line, Character: 1}}}
}
