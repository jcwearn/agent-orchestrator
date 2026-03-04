package orchestrator

import (
	"context"
	"errors"
	"time"

	gogithub "github.com/google/go-github/v83/github"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

const (
	StatusQueued           = "queued"
	StatusPlanning         = "planning"
	StatusAwaitingApproval = "awaiting_approval"
	StatusImplementing     = "implementing"
	StatusComplete         = "complete"
	StatusFailed           = "failed"
	StatusCancelled        = "cancelled"
)

// isRateLimitError checks if err is a GitHub rate limit error and returns the
// reset time if so.
func isRateLimitError(err error) (time.Time, bool) {
	var rlErr *gogithub.RateLimitError
	if errors.As(err, &rlErr) {
		return rlErr.Rate.Reset.Time, true
	}
	var abuseErr *gogithub.AbuseRateLimitError
	if errors.As(err, &abuseErr) {
		retryAfter := abuseErr.GetRetryAfter()
		if retryAfter > 0 {
			return time.Now().Add(retryAfter), true
		}
		return time.Now().Add(1 * time.Minute), true
	}
	return time.Time{}, false
}

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

// needsReplan returns true if the task has non-empty, non-approved feedback
// that requires re-running the plan agent.
func needsReplan(task *store.Task) bool {
	return task.PlanFeedback != nil &&
		*task.PlanFeedback != "approved" &&
		*task.PlanFeedback != ""
}
