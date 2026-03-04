import { useCallback, useEffect, useState } from "react"
import { Link, useParams } from "react-router-dom"
import type { Task, WSEvent } from "@/types/api"
import { getTask } from "@/api/client"
import { StatusBadge } from "@/components/StatusBadge"
import { TimeAgo } from "@/components/TimeAgo"
import { PlanView } from "@/components/PlanView"
import { ApprovalForm } from "@/components/ApprovalForm"
import { LogViewer } from "@/components/LogViewer"
import { useLogStream } from "@/hooks/useLogStream"

interface TaskDetailProps {
  subscribe: (fn: (e: WSEvent) => void) => () => void
}

export function TaskDetail({ subscribe }: TaskDetailProps) {
  const { id } = useParams<{ id: string }>()
  const [task, setTask] = useState<Task | null>(null)
  const [loading, setLoading] = useState(true)
  const { lines, done } = useLogStream(id)

  const fetchTask = useCallback(async () => {
    if (!id) return
    try {
      const data = await getTask(id)
      setTask(data)
    } finally {
      setLoading(false)
    }
  }, [id])

  useEffect(() => {
    fetchTask()
  }, [fetchTask])

  useEffect(() => {
    return subscribe((event: WSEvent) => {
      if (event.task_id === id && event.type === "task.updated" && event.data) {
        setTask(event.data)
      }
    })
  }, [subscribe, id])

  if (loading) {
    return <p className="text-zinc-500">Loading task...</p>
  }
  if (!task) {
    return <p className="text-zinc-500">Task not found.</p>
  }

  return (
    <div className="space-y-6">
      <div>
        <Link to="/tasks" className="text-sm text-zinc-400 hover:text-zinc-200">
          &larr; Back to tasks
        </Link>
      </div>

      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <h1 className="text-2xl font-semibold text-zinc-100 break-words">
            {task.title ?? task.prompt}
          </h1>
          {task.title && (
            <p className="mt-2 text-sm text-zinc-300 whitespace-pre-wrap">{task.prompt}</p>
          )}
          <div className="mt-2 flex flex-wrap items-center gap-3 text-sm text-zinc-400">
            <StatusBadge status={task.status} />
            <span>{task.repo_url.replace(/^https?:\/\/github\.com\//, "")}</span>
            <span>Branch: {task.base_branch}</span>
            <span>
              Created <TimeAgo date={task.created_at} />
            </span>
            {task.workspace_id && <span>Workspace: {task.workspace_id}</span>}
          </div>
        </div>
      </div>

      {task.pr_url && (
        <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-4">
          <p className="text-sm text-green-400">
            Pull request opened:{" "}
            <a
              href={task.pr_url}
              target="_blank"
              rel="noopener noreferrer"
              className="underline hover:text-green-300"
            >
              {task.pr_url}
            </a>
          </p>
        </div>
      )}

      {task.error_message && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-4">
          <p className="text-sm text-red-400">{task.error_message}</p>
        </div>
      )}

      {task.status === "awaiting_approval" && (
        <ApprovalForm taskId={task.id} plan={task.plan} onAction={fetchTask} />
      )}

      <PlanView plan={task.plan} revision={task.plan_revision} />

      <LogViewer lines={lines} done={done} />
    </div>
  )
}
