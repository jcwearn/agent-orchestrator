import { useCallback, useEffect, useRef, useState } from "react"
import type { WSEvent } from "@/types/api"

type Listener = (event: WSEvent) => void

function getWSUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:"
  return `${proto}//${window.location.host}/api/v1/ws`
}

export function useWebSocket() {
  const wsRef = useRef<WebSocket | null>(null)
  const listenersRef = useRef<Set<Listener>>(new Set())
  const retryRef = useRef(0)
  const [connected, setConnected] = useState(false)

  const connect = useCallback(() => {
    const ws = new WebSocket(getWSUrl())
    wsRef.current = ws

    ws.onopen = () => {
      retryRef.current = 0
      setConnected(true)
    }

    ws.onmessage = (e) => {
      try {
        const event: WSEvent = JSON.parse(e.data)
        listenersRef.current.forEach((fn) => fn(event))
      } catch {
        // ignore malformed messages
      }
    }

    ws.onclose = () => {
      setConnected(false)
      const delay = Math.min(1000 * 2 ** retryRef.current, 30000)
      retryRef.current++
      setTimeout(connect, delay)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [])

  useEffect(() => {
    connect()
    return () => {
      wsRef.current?.close()
    }
  }, [connect])

  const subscribe = useCallback((fn: Listener) => {
    listenersRef.current.add(fn)
    return () => {
      listenersRef.current.delete(fn)
    }
  }, [])

  return { subscribe, connected }
}
