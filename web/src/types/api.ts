export interface Task {
  id: string
  status: string
  title: string | null
  prompt: string
  plan: string | null
  plan_feedback: string | null
  repo_url: string
  base_branch: string
  source_type: string
  github_owner: string | null
  github_repo: string | null
  github_issue: number | null
  session_id: string
  workspace_id: string | null
  current_step: string | null
  plan_comment_id: number | null
  plan_revision: number
  pr_url: string | null
  pr_number: number | null
  run_tests: boolean
  decisions: string | null
  created_at: string
  started_at: string | null
  completed_at: string | null
  error_message: string | null
}

export interface TaskLog {
  id: number
  task_id: string
  step: string
  stream: string
  line: string
  created_at: string
}

export interface AgentInfo {
  name: string
  task_id: string
  task_title: string
  workspace_status: string
}

export interface WSEvent {
  type: string
  task_id: string
  data?: Task
  agents?: AgentInfo[]
}

export interface CreateTaskRequest {
  title?: string
  prompt: string
  repo_url: string
  base_branch: string
}

export interface ConfigResponse {
  github_configured: boolean
  auto_create_issues: boolean
}

export interface RepoInfo {
  full_name: string
  clone_url: string
}

export interface AuthUser {
  id: string
  username: string
  role: string
}

export interface AuthStatus {
  setup_required: boolean
  authenticated: boolean
  user: AuthUser | null
}

export interface LoginRequest {
  username: string
  password: string
}

export interface SetupRequest {
  username: string
  password: string
}
