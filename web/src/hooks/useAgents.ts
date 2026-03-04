import { useCallback, useEffect, useState } from "react"
import type { AgentInfo, WSEvent } from "@/types/api"
import { listAgents } from "@/api/client"

export function useAgents(subscribe: (fn: (e: WSEvent) => void) => () => void) {
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [loading, setLoading] = useState(true)

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
      } else if (event.type === "agent.updated") {
        fetchAgents()
      } else if (event.type.startsWith("task.")) {
        fetchAgents()
      }
    })
  }, [subscribe, fetchAgents])

  // Poll every 30s to catch workspace changes from outside the orchestrator.
  useEffect(() => {
    const id = setInterval(fetchAgents, 30_000)
    return () => clearInterval(id)
  }, [fetchAgents])

  return { agents, loading }
}
