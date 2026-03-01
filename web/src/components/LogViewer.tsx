import { useEffect, useRef } from "react"
import { cn } from "@/lib/utils"
import type { TaskLog } from "@/types/api"

interface LogViewerProps {
  lines: TaskLog[]
  done: boolean
}

export function LogViewer({ lines, done }: LogViewerProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const autoScrollRef = useRef(true)

  useEffect(() => {
    const el = containerRef.current
    if (!el || !autoScrollRef.current) return
    el.scrollTop = el.scrollHeight
  }, [lines])

  function handleScroll() {
    const el = containerRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    autoScrollRef.current = atBottom
  }

  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950">
      <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-2">
        <span className="text-sm font-medium text-zinc-400">Logs</span>
        {!done && (
          <span className="flex items-center gap-2 text-xs text-zinc-500">
            <span className="h-2 w-2 animate-pulse rounded-full bg-green-500" />
            Streaming
          </span>
        )}
        {done && (
          <span className="text-xs text-zinc-500">Stream complete</span>
        )}
      </div>
      <div
        ref={containerRef}
        onScroll={handleScroll}
        className="h-[480px] overflow-y-auto p-4 font-mono text-sm"
      >
        {lines.length === 0 && (
          <p className="text-zinc-600">No log output yet.</p>
        )}
        {lines.map((line) => (
          <div
            key={line.id}
            className={cn(
              "whitespace-pre-wrap break-all leading-6",
              line.stream === "stderr" ? "text-red-400" : "text-zinc-300",
            )}
          >
            {line.line}
          </div>
        ))}
      </div>
    </div>
  )
}
