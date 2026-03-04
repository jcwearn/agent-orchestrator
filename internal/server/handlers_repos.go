package server

import (
	"net/http"
	"sync"
	"time"

	gogithub "github.com/google/go-github/v83/github"
)

type RepoInfo struct {
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
}

const (
	repoCacheTTL = 5 * time.Minute
	repoMaxCount = 500
)

type repoCache struct {
	mu      sync.Mutex
	repos   []RepoInfo
	expires time.Time
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	if s.githubClient == nil {
		writeJSON(w, http.StatusOK, []RepoInfo{})
		return
	}

	s.repoCache.mu.Lock()
	if time.Now().Before(s.repoCache.expires) {
		cached := s.repoCache.repos
		s.repoCache.mu.Unlock()
		writeJSON(w, http.StatusOK, cached)
		return
	}
	s.repoCache.mu.Unlock()

	var repos []RepoInfo
	opts := &gogithub.ListOptions{PerPage: 100}

	for {
		result, resp, err := s.githubClient.Apps.ListRepos(r.Context(), opts)
		if err != nil {
			s.logger.Error("list github repos", "error", err)
			writeError(w, http.StatusBadGateway, "failed to list repositories")
			return
		}

		for _, repo := range result.Repositories {
			repos = append(repos, RepoInfo{
				FullName: repo.GetFullName(),
				CloneURL: repo.GetCloneURL(),
			})
			if len(repos) >= repoMaxCount {
				break
			}
		}

		if len(repos) >= repoMaxCount || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if repos == nil {
		repos = []RepoInfo{}
	}

	s.repoCache.mu.Lock()
	s.repoCache.repos = repos
	s.repoCache.expires = time.Now().Add(repoCacheTTL)
	s.repoCache.mu.Unlock()

	writeJSON(w, http.StatusOK, repos)
}
