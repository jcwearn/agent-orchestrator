CREATE UNIQUE INDEX idx_tasks_github_issue_unique
ON tasks(github_owner, github_repo, github_issue)
WHERE status != 'failed';
