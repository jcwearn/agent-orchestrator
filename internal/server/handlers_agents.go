package server

import (
	"net/http"
	"sort"
)

type AgentInfo struct {
	Name            string `json:"name"`
	TaskID          string `json:"task_id"`
	TaskTitle       string `json:"task_title"`
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
		info := AgentInfo{
			Name:            slot.Name,
			TaskID:          slot.TaskID,
			WorkspaceStatus: statusMap[slot.Name],
		}
		if slot.TaskID != "" {
			if task, err := s.store.GetTask(r.Context(), slot.TaskID); err == nil && task.Title != nil {
				info.TaskTitle = *task.Title
			}
		}
		agents = append(agents, info)
	}

	sort.Slice(agents, func(i, j int) bool {
		pi, pj := statusPriority(agents[i].WorkspaceStatus), statusPriority(agents[j].WorkspaceStatus)
		if pi != pj {
			return pi < pj
		}
		return agents[i].Name < agents[j].Name
	})

	writeJSON(w, http.StatusOK, agents)
}

func statusPriority(status string) int {
	switch status {
	case "running", "starting", "stopping":
		return 0
	default:
		return 1
	}
}
