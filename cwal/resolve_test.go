package cwal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestResolveToons(t *testing.T) {
	fixture, err := os.ReadFile("testdata/handles_11682563.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/account/11682563/handles" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	resolver := NewResolver()
	resolver.baseURL = srv.URL

	toons, err := resolver.ResolveToons(11682563)
	if err != nil {
		t.Fatalf("ResolveToons: %v", err)
	}
	if len(toons) == 0 {
		t.Fatal("expected at least one toon for Larva")
	}

	found := false
	for _, toon := range toons {
		if toon.Handle == "jsa_larva" || toon.Handle == "JSA_Larva" {
			found = true
		}
		if toon.Gateway == 0 {
			t.Errorf("toon %q has zero gateway", toon.Handle)
		}
	}
	if !found {
		t.Errorf("expected to find jsa_larva handle, got: %v", toons)
	}
}

func TestResolveToons404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", 404)
	}))
	defer srv.Close()

	resolver := NewResolver()
	resolver.baseURL = srv.URL

	_, err := resolver.ResolveToons(99999)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestResolveToonsEmptyHandles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"handles":[]}`))
	}))
	defer srv.Close()

	resolver := NewResolver()
	resolver.baseURL = srv.URL

	toons, err := resolver.ResolveToons(12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(toons) != 0 {
		t.Fatalf("expected 0 toons, got %d", len(toons))
	}
}
