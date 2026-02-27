package store

import "time"

type Task struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	Prompt        string     `json:"prompt"`
	Plan          *string    `json:"plan"`
	PlanFeedback  *string    `json:"plan_feedback"`
	RepoURL       string     `json:"repo_url"`
	BaseBranch    string     `json:"base_branch"`
	SourceType    string     `json:"source_type"`
	GithubOwner   *string    `json:"github_owner"`
	GithubRepo    *string    `json:"github_repo"`
	GithubIssue   *int       `json:"github_issue"`
	SessionID     string     `json:"session_id"`
	WorkspaceID   *string    `json:"workspace_id"`
	CurrentStep   *string    `json:"current_step"`
	PlanCommentID *int       `json:"plan_comment_id"`
	PlanRevision  int        `json:"plan_revision"`
	PRUrl         *string    `json:"pr_url"`
	PRNumber      *int       `json:"pr_number"`
	RunTests      bool       `json:"run_tests"`
	Decisions     *string    `json:"decisions"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	ErrorMessage  *string    `json:"error_message"`
}

type TaskLog struct {
	ID        int       `json:"id"`
	TaskID    string    `json:"task_id"`
	Step      string    `json:"step"`
	Stream    string    `json:"stream"`
	Line      string    `json:"line"`
	CreatedAt time.Time `json:"created_at"`
}
