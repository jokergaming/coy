package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchCatalogue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.URL.Query().Get("tenant") != "demo" || r.Header.Get("Authorization") != "Bearer fictional-key" {
			t.Error("incorrect discovery request")
		}
		fmt.Fprint(w, `{"data":[{"id":"vendor/model-a","name":"Model A"},{"id":"model-b"}]}`)
	}))
	defer server.Close()
	models, err := Fetch(t.Context(), server.URL+"/v1/?tenant=demo", "fictional-key")
	if err != nil || len(models) != 2 || models[0].ID != "vendor/model-a" || models[0].Name != "Model A" {
		t.Fatalf("unexpected catalogue: %v", err)
	}
}

func TestFetchErrorsAreExplicitAndRedacted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", 401, "fictional-secret", "HTTP 401"},
		{"malformed", 200, `{"data":"fictional-secret"}`, "格式无效"},
		{"missing-data", 200, `{}`, "格式无效"},
		{"null-data", 200, `{"data":null}`, "格式无效"},
		{"pagination", 200, `{"data":[{"id":"a"}],"has_more":true}`, "分页"},
		{"too-large", 200, strings.Repeat("x", (8<<20)+1), "8 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := Fetch(t.Context(), server.URL+"/v1?token=fictional-secret", "fictional-secret")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "fictional-secret") {
				t.Fatal("missing error or leaked credentials")
			}
		})
	}
}

func TestFetchRejectsRedirectAndCancellation(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := Fetch(t.Context(), redirect.URL, "fictional-key"); err == nil || reached {
		t.Fatal("redirect should not forward credentials")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Fetch(ctx, target.URL, ""); err == nil || reached {
		t.Fatal("cancelled discovery should fail")
	}
}
