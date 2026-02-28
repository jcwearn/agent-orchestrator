package server

import (
	"net/http"
)

type AgentInfo struct {
	Name            string `json:"name"`
	TaskID          string `json:"task_id"`
	WorkspaceStatus string `json:"workspace_status"`
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	slots := s.pool.Status()

	statusMap := make(map[string]string)
	workspaces, err := s.executor.ListWorkspaces(r.Context())
	if err != nil {
		s.logger.Warn("list workspaces for agents", "error", err)
	} else {
		for _, ws := range workspaces {
			statusMap[ws.Name] = string(ws.Status)
		}
	}

	agents := make([]AgentInfo, 0, len(slots))
	for _, slot := range slots {
		agents = append(agents, AgentInfo{
			Name:            slot.Name,
			TaskID:          slot.TaskID,
			WorkspaceStatus: statusMap[slot.Name],
		})
	}

	writeJSON(w, http.StatusOK, agents)
}
