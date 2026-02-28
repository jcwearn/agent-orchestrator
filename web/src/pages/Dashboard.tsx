import { Link } from "react-router-dom"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Badge } from "@/components/ui/badge"
import { useAgents } from "@/hooks/useAgents"
import type { WSEvent } from "@/types/api"

const wsStatusConfig: Record<string, { label: string; className: string }> = {
  running: {
    label: "Running",
    className: "bg-green-500/20 text-green-400 hover:bg-green-500/20",
  },
  stopped: {
    label: "Stopped",
    className: "bg-zinc-700 text-zinc-300 hover:bg-zinc-700",
  },
  starting: {
    label: "Starting",
    className: "bg-blue-500/20 text-blue-400 hover:bg-blue-500/20",
  },
  stopping: {
    label: "Stopping",
    className: "bg-amber-500/20 text-amber-400 hover:bg-amber-500/20",
  },
}

interface DashboardProps {
  subscribe: (fn: (e: WSEvent) => void) => () => void
}

export function Dashboard({ subscribe }: DashboardProps) {
  const { agents, loading } = useAgents(subscribe)

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-zinc-100">Agents</h1>
      </div>
      <div className="rounded-lg border border-zinc-800 bg-zinc-900">
        <Table>
          <TableHeader>
            <TableRow className="border-zinc-800 hover:bg-transparent">
              <TableHead className="text-zinc-400">Name</TableHead>
              <TableHead className="text-zinc-400">Workspace Status</TableHead>
              <TableHead className="text-zinc-400">Current Task</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && (
              <TableRow className="border-zinc-800">
                <TableCell colSpan={3} className="h-[72px] text-center text-zinc-500">
                  Loading agents...
                </TableCell>
              </TableRow>
            )}
            {!loading && agents.length === 0 && (
              <TableRow className="border-zinc-800">
                <TableCell colSpan={3} className="h-[72px] text-center text-zinc-500">
                  No agents configured.
                </TableCell>
              </TableRow>
            )}
            {agents.map((agent) => {
              const wsConfig = wsStatusConfig[agent.workspace_status] ?? {
                label: agent.workspace_status || "Unknown",
                className: "bg-zinc-700 text-zinc-300 hover:bg-zinc-700",
              }
              return (
                <TableRow key={agent.name} className="h-[72px] border-zinc-800">
                  <TableCell className="font-medium text-zinc-100">
                    {agent.name}
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary" className={wsConfig.className}>
                      {wsConfig.label}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {agent.task_id ? (
                      <Link
                        to={`/tasks/${agent.task_id}`}
                        className="text-sm text-sky-400 hover:underline"
                      >
                        {agent.task_id.slice(0, 8)}...
                      </Link>
                    ) : (
                      <span className="text-sm text-zinc-500">Idle</span>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
