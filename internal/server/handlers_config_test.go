package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetConfig_WithoutGitHub(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var config ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	if config.GitHubConfigured {
		t.Fatal("expected github_configured to be false")
	}
}

func TestGetConfig_WithGitHub(t *testing.T) {
	ghServer := httptest.NewServer(http.NewServeMux())
	defer ghServer.Close()

	srv, _ := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var config ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	if !config.GitHubConfigured {
		t.Fatal("expected github_configured to be true")
	}
}
