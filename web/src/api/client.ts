import type {
  AgentInfo,
  AuthStatus,
  ConfigResponse,
  CreateTaskRequest,
  LoginRequest,
  RepoInfo,
  SetupRequest,
  Task,
} from "@/types/api"

const BASE = "/api/v1"

const AUTH_PATHS = ["/auth/status", "/auth/login", "/auth/logout", "/auth/setup"]

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  })
  if (!res.ok) {
    if (res.status === 401 && !AUTH_PATHS.includes(path)) {
      window.location.href = "/login"
      throw new Error("authentication required")
    }
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export function getAuthStatus(): Promise<AuthStatus> {
  return request<AuthStatus>("/auth/status")
}

export function login(req: LoginRequest): Promise<AuthStatus> {
  return request<AuthStatus>("/auth/login", {
    method: "POST",
    body: JSON.stringify(req),
  })
}

export function logout(): Promise<void> {
  return request<void>("/auth/logout", { method: "POST" })
}

export function setup(req: SetupRequest): Promise<AuthStatus> {
  return request<AuthStatus>("/auth/setup", {
    method: "POST",
    body: JSON.stringify(req),
  })
}

export function getConfig(): Promise<ConfigResponse> {
  return request<ConfigResponse>("/config")
}

export function listTasks(status?: string): Promise<Task[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : ""
  return request<Task[]>(`/tasks${qs}`)
}

export function getTask(id: string): Promise<Task> {
  return request<Task>(`/tasks/${id}`)
}

export function createTask(req: CreateTaskRequest): Promise<Task> {
  return request<Task>("/tasks", {
    method: "POST",
    body: JSON.stringify(req),
  })
}

export function deleteTask(id: string): Promise<void> {
  return request<void>(`/tasks/${id}`, { method: "DELETE" })
}

export function approveTask(
  id: string,
  opts?: { run_tests?: boolean; decisions?: string },
): Promise<Task> {
  return request<Task>(`/tasks/${id}/approve`, {
    method: "POST",
    body: JSON.stringify(opts ?? {}),
  })
}

export function sendFeedback(
  id: string,
  feedback: string,
  decisions?: string,
): Promise<Task> {
  return request<Task>(`/tasks/${id}/feedback`, {
    method: "POST",
    body: JSON.stringify({ feedback, decisions }),
  })
}

export function cancelTask(id: string): Promise<Task> {
  return request<Task>(`/tasks/${id}/cancel`, { method: "POST" })
}

export function listAgents(): Promise<AgentInfo[]> {
  return request<AgentInfo[]>("/agents")
}

export function listRepositories(): Promise<RepoInfo[]> {
  return request<RepoInfo[]>("/repositories")
}
