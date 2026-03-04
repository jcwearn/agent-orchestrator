package server

import "net/http"

type ConfigResponse struct {
	GitHubConfigured bool `json:"github_configured"`
	AutoCreateIssues bool `json:"auto_create_issues"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ConfigResponse{
		GitHubConfigured: s.githubClient != nil,
		AutoCreateIssues: s.autoCreateIssues,
	})
}
