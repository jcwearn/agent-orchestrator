package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	gogithub "github.com/google/go-github/v84/github"
)

func TestListRepos_Unauthenticated(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestListRepos_WithoutGitHub(t *testing.T) {
	srv, s := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	client := authenticatedClient(t, s, ts.URL)

	resp, err := client.Get(ts.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var repos []RepoInfo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected empty array, got %d repos", len(repos))
	}
}

func TestListRepos_WithGitHub(t *testing.T) {
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("GET /api/v3/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.ListRepositories{
			TotalCount: gogithub.Ptr(2),
			Repositories: []*gogithub.Repository{
				{
					FullName: gogithub.Ptr("owner/repo-one"),
					CloneURL: gogithub.Ptr("https://github.com/owner/repo-one.git"),
				},
				{
					FullName: gogithub.Ptr("owner/repo-two"),
					CloneURL: gogithub.Ptr("https://github.com/owner/repo-two.git"),
				},
			},
		})
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	client := authenticatedClient(t, s, ts.URL)

	resp, err := client.Get(ts.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var repos []RepoInfo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if repos[0].FullName != "owner/repo-one" {
		t.Fatalf("expected full_name 'owner/repo-one', got %q", repos[0].FullName)
	}
	if repos[0].CloneURL != "https://github.com/owner/repo-one.git" {
		t.Fatalf("expected clone_url with .git, got %q", repos[0].CloneURL)
	}
}

func TestListRepos_GitHubAPIError(t *testing.T) {
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("GET /api/v3/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintln(w, `{"message":"internal error"}`)
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	client := authenticatedClient(t, s, ts.URL)

	resp, err := client.Get(ts.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
}

func TestListRepos_CachesResults(t *testing.T) {
	callCount := 0
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("GET /api/v3/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.ListRepositories{
			TotalCount: gogithub.Ptr(1),
			Repositories: []*gogithub.Repository{
				{
					FullName: gogithub.Ptr("owner/cached-repo"),
					CloneURL: gogithub.Ptr("https://github.com/owner/cached-repo.git"),
				},
			},
		})
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	client := authenticatedClient(t, s, ts.URL)

	// First request should hit GitHub API.
	resp, err := client.Get(ts.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if callCount != 1 {
		t.Fatalf("expected 1 API call, got %d", callCount)
	}

	// Second request should use cache.
	resp, err = client.Get(ts.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if callCount != 1 {
		t.Fatalf("expected 1 API call (cached), got %d", callCount)
	}
}
