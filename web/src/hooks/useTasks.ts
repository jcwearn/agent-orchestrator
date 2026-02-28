import { useCallback, useEffect, useState } from "react"
import type { Task, WSEvent } from "@/types/api"
import { listTasks } from "@/api/client"

export function useTasks(
  subscribe: (fn: (e: WSEvent) => void) => () => void,
  statusFilter?: string,
) {
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)

  const fetchTasks = useCallback(async () => {
    try {
      const data = await listTasks(statusFilter)
      setTasks(data)
    } catch {
      // silently ignore
    } finally {
      setLoading(false)
    }
  }, [statusFilter])

  useEffect(() => {
    fetchTasks()
  }, [fetchTasks])

  useEffect(() => {
    return subscribe((event: WSEvent) => {
      if (event.type === "task.created" && event.data) {
        setTasks((prev) => [event.data!, ...prev])
      } else if (event.type === "task.updated" && event.data) {
        setTasks((prev) =>
          prev.map((t) => (t.id === event.task_id ? event.data! : t)),
        )
      } else if (event.type === "task.deleted") {
        setTasks((prev) => prev.filter((t) => t.id !== event.task_id))
      }
    })
  }, [subscribe])

  return { tasks, loading, refetch: fetchTasks }
}
