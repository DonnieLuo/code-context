package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/code-context/internal/tools"
)

var toolNames = []string{"search_code", "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_call_graph", "trace_call_path", "read_file", "list_files", "git_query", "search_symbols", "get_file_symbols", "get_symbol_context"}

func TestToolAcceptsBatchAndKeepsPerItemErrors(t *testing.T) {
	server := New(nil, time.Second, 2).Handler()
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/search_code", bytes.NewBufferString(`{"requests":[{"repo_id":"r"},{"repo_id":"r"}]}`))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	want := `{"results":[{"truncated":false,"error":{"code":"tool_error","message":"query is required"}},{"truncated":false,"error":{"code":"tool_error","message":"query is required"}}]}` + "\n"
	if recorder.Body.String() != want {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestToolRejectsEmptyBatch(t *testing.T) {
	server := New(nil, time.Second, 2).Handler()
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/search_code", bytes.NewBufferString(`{"requests":[]}`))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestReplacedToolsAreNotExposed(t *testing.T) {
	server := New(nil, time.Second, 2).Handler()
	for _, name := range []string{"find_callers", "find_callees", "find_implementations", "get_hover", "get_git_diff"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/tools/"+name, nil)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d", name, recorder.Code)
		}
	}
}

func TestV2EveryToolAcceptsDirectRequest(t *testing.T) {
	server := New(nil, time.Second, 2).Handler()
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v2/tools/"+name, bytes.NewBufferString(`{}`))
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var body struct {
				Error *toolItemError `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error == nil || body.Error.Message != "repo_id is required" {
				t.Fatalf("unexpected body: %s", recorder.Body.String())
			}
		})
	}
}

func TestV2SchemasUseOnlyStringAndNumberParameters(t *testing.T) {
	server := New(nil, time.Second, 2).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v2/tools", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Tools []struct {
			Name       string        `json:"name"`
			Parameters []v2Parameter `json:"parameters"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tools) != len(toolNames) {
		t.Fatalf("tool count = %d", len(response.Tools))
	}
	for _, tool := range response.Tools {
		for _, parameter := range tool.Parameters {
			if parameter.Type != "string" && parameter.Type != "number" {
				t.Fatalf("%s.%s type = %q", tool.Name, parameter.Name, parameter.Type)
			}
		}
	}
}

func TestNormalizeV2SearchCodeProducesCompactMatches(t *testing.T) {
	events := []map[string]any{
		{"type": "begin", "data": map[string]any{"path": map[string]any{"text": "src/Order.java"}}},
		{"type": "context", "data": map[string]any{"path": map[string]any{"text": "src/Order.java"}, "lines": map[string]any{"text": "void run() {\n"}, "line_number": float64(9)}},
		{
			"type": "match",
			"data": map[string]any{
				"path":        map[string]any{"text": "src/Order.java"},
				"lines":       map[string]any{"text": "  createOrder();\n"},
				"line_number": float64(10),
				"submatches":  []any{map[string]any{"match": map[string]any{"text": "createOrder"}, "start": float64(2)}},
			},
		},
		{"type": "context", "data": map[string]any{"path": map[string]any{"text": "src/Order.java"}, "lines": map[string]any{"text": "}\n"}, "line_number": float64(11)}},
	}
	data, truncated := normalizeV2Result("search_code", events, 10)
	matches, ok := data.([]v2SearchMatch)
	if !ok || truncated || len(matches) != 1 {
		t.Fatalf("data=%#v truncated=%v", data, truncated)
	}
	got := matches[0]
	if got.File != "src/Order.java" || got.Line != 10 || got.Column != 3 || got.Match != "createOrder" {
		t.Fatalf("unexpected match: %#v", got)
	}
	if got.Snippet != "9: void run() {\n10:   createOrder();\n11: }" {
		t.Fatalf("unexpected snippet: %q", got.Snippet)
	}
}

func TestNormalizeV2CollectionsHonorsLimit(t *testing.T) {
	data, truncated := normalizeV2Result("find_references", []tools.Location{{File: "A.java"}, {File: "B.java"}}, 1)
	locations, ok := data.([]tools.Location)
	if !ok || !truncated || len(locations) != 1 || locations[0].File != "A.java" {
		t.Fatalf("data=%#v truncated=%v", data, truncated)
	}

	data, truncated = normalizeV2Result("get_file_symbols", []any{
		map[string]any{"name": "OrderService", "children": []any{map[string]any{"name": "createOrder"}}},
	}, 1)
	items, ok := data.([]map[string]any)
	if !ok || !truncated || len(items) != 1 || items[0]["name"] != "OrderService" {
		t.Fatalf("data=%#v truncated=%v", data, truncated)
	}
	if _, hasChildren := items[0]["children"]; hasChildren {
		t.Fatalf("flattened item still contains children: %#v", items[0])
	}
}

func TestNormalizeV2SymbolsIncludesSemanticMetadata(t *testing.T) {
	data, truncated := normalizeV2Result("search_symbols", []tools.Location{
		{File: "OrderService.java", StartLine: 42, StartColumn: 3, EndLine: 42, EndColumn: 14, Snippet: "createOrder", SymbolKind: 6, SymbolType: "method", Container: "OrderService"},
		{File: "Other.java", Snippet: "createOrder"},
	}, 1)
	symbols, ok := data.([]v2Symbol)
	if !ok || !truncated || len(symbols) != 1 {
		t.Fatalf("data=%#v truncated=%v", data, truncated)
	}
	got := symbols[0]
	if got.Name != "createOrder" || got.Kind != "method" || got.KindCode != 6 || got.Container != "OrderService" || got.StartLine != 42 {
		t.Fatalf("unexpected symbol: %#v", got)
	}
}

func TestV2CollectionSchemasExposeLimit(t *testing.T) {
	for _, name := range []string{"search_code", "search_symbols", "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_file_symbols", "list_files"} {
		found := false
		for _, parameter := range v2ToolParameters(name) {
			if parameter.Name == "limit" && parameter.Type == "number" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not expose a numeric limit", name)
		}
	}
}
