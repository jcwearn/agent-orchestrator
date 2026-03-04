import { useCallback, useEffect, useRef, useState } from "react"
import type { AgentInfo, WSEvent } from "@/types/api"
import { listAgents } from "@/api/client"

export function useAgents(subscribe: (fn: (e: WSEvent) => void) => () => void) {
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [loading, setLoading] = useState(true)
  const intervalRef = useRef<ReturnType<typeof setInterval>>()

  const fetchAgents = useCallback(async () => {
    try {
      const data = await listAgents()
      setAgents(data)
    } catch {
      // silently ignore — will retry on next event
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchAgents()
  }, [fetchAgents])

  useEffect(() => {
    return subscribe((event: WSEvent) => {
      if (event.type === "agent.updated" && event.agents) {
        setAgents(event.agents)
      } else if (event.type.startsWith("task.")) {
        fetchAgents()
      }

      clearInterval(intervalRef.current)
      intervalRef.current = setInterval(fetchAgents, 30_000)
    })
  }, [subscribe, fetchAgents])

  // Poll every 30s to catch workspace changes from outside the orchestrator.
  useEffect(() => {
    intervalRef.current = setInterval(fetchAgents, 30_000)
    return () => clearInterval(intervalRef.current)
  }, [fetchAgents])

  return { agents, loading }
}
