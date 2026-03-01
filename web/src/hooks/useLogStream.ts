import { useEffect, useReducer, useRef, useState } from "react"
import type { TaskLog } from "@/types/api"

interface LogState {
  lines: TaskLog[]
}

type LogAction =
  | { type: "append"; line: TaskLog }
  | { type: "reset" }

function logReducer(state: LogState, action: LogAction): LogState {
  switch (action.type) {
    case "append":
      return { lines: [...state.lines, action.line] }
    case "reset":
      return { lines: [] }
  }
}

export function useLogStream(taskId: string | undefined) {
  const [state, dispatch] = useReducer(logReducer, { lines: [] })
  const [done, setDone] = useState(false)
  const eventSourceRef = useRef<EventSource | null>(null)

  useEffect(() => {
    if (!taskId) return

    dispatch({ type: "reset" })
    setDone(false)

    const es = new EventSource(`/api/v1/tasks/${taskId}/logs`)
    eventSourceRef.current = es

    es.onmessage = (e) => {
      try {
        const log: TaskLog = JSON.parse(e.data)
        dispatch({ type: "append", line: log })
      } catch {
        // ignore
      }
    }

    es.addEventListener("done", () => {
      setDone(true)
      es.close()
    })

    es.onerror = () => {
      es.close()
      setDone(true)
    }

    return () => {
      es.close()
    }
  }, [taskId])

  return { lines: state.lines, done }
}
