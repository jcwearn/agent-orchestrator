package orchestrator

import (
	"context"

	"github.com/jcwearn/agent-orchestrator/internal/store"
)

const (
	StatusQueued           = "queued"
	StatusPlanning         = "planning"
	StatusAwaitingApproval = "awaiting_approval"
	StatusImplementing     = "implementing"
	StatusComplete         = "complete"
	StatusFailed           = "failed"
)

var approvedValue = "approved"

// nextTask returns the oldest queued task (FIFO).
// ListTasks returns DESC order, so we take the last element.
func (o *Orchestrator) nextTask(ctx context.Context) (*store.Task, error) {
	tasks, err := o.store.ListTasks(ctx, StatusQueued)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	return &tasks[len(tasks)-1], nil
}

// activeTasks returns all tasks currently in planning or implementing status.
func (o *Orchestrator) activeTasks(ctx context.Context) ([]store.Task, error) {
	planning, err := o.store.ListTasks(ctx, StatusPlanning)
	if err != nil {
		return nil, err
	}
	implementing, err := o.store.ListTasks(ctx, StatusImplementing)
	if err != nil {
		return nil, err
	}
	return append(planning, implementing...), nil
}

// isApproved returns true if the task has been approved for implementation.
func isApproved(task *store.Task) bool {
	return task.PlanFeedback != nil && *task.PlanFeedback == "approved"
}
