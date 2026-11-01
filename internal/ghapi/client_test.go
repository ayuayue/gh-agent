package ghapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloseIssue(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c := New("token")
	c.SetBaseURL(srv.URL)
	if err := c.CloseIssue(context.Background(), "o/r", 7, "not_planned"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if method != http.MethodPatch || path != "/repos/o/r/issues/7" {
		t.Errorf("请求不对：%s %s", method, path)
	}
	if body["state"] != "closed" || body["state_reason"] != "not_planned" {
		t.Errorf("body 不对：%v", body)
	}

	body = nil
	if err := c.CloseIssue(context.Background(), "o/r", 7, ""); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if _, ok := body["state_reason"]; ok {
		t.Errorf("PR 不该带 state_reason：%v", body)
	}
}
