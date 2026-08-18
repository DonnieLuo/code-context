package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
