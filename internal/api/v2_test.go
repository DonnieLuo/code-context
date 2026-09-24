package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/code-context/internal/v2"
)

func TestV2DoesNotChangeV1Discovery(t *testing.T) {
	old := New(nil, time.Second, 2).Handler()
	withV2 := New(nil, time.Second, 2).WithV2(&v2.Service{}).Handler()
	get := func(h http.Handler, path string) string {
		t.Helper()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("%s: %d", path, r.Code)
		}
		return r.Body.String()
	}
	if get(old, "/v1/tools") != get(withV2, "/v1/tools") {
		t.Fatal("v1 discovery changed")
	}
	var schema struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(get(withV2, "/v2/tools")), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Tools) != 14 || schema.Tools[0].Name != "build_context" {
		t.Fatalf("v2 tools: %+v", schema.Tools)
	}
}
func TestV2RejectsMalformedBatchWithoutCallingService(t *testing.T) {
	h := New(nil, time.Second, 2).WithV2(&v2.Service{}).Handler()
	for _, body := range []string{`{"requests":[]}`, `{"requests":[{}, {}, {}]}`, `{"requests":[{}],"unknown":true}`, `{"requests":[{}]} {}`} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", "/v2/tools/build_context", bytes.NewBufferString(body)))
		if r.Code != 400 {
			t.Fatalf("%s => %d %s", body, r.Code, r.Body.String())
		}
	}
}
