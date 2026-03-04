import type { AgentInfo, CreateTaskRequest, Task } from "@/types/api"

const BASE = "/api/v1"

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
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

export function listAgents(): Promise<AgentInfo[]> {
  return request<AgentInfo[]>("/agents")
}
