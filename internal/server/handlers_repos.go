package server

import (
	"net/http"

	gogithub "github.com/google/go-github/v83/github"
)

type RepoInfo struct {
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	if s.githubClient == nil {
		writeJSON(w, http.StatusOK, []RepoInfo{})
		return
	}

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
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if repos == nil {
		repos = []RepoInfo{}
	}

	writeJSON(w, http.StatusOK, repos)
}
