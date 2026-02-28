import { Link } from "react-router-dom"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { StatusBadge } from "@/components/StatusBadge"
import { TimeAgo } from "@/components/TimeAgo"
import { useTasks } from "@/hooks/useTasks"
import type { WSEvent } from "@/types/api"

interface TaskListProps {
  subscribe: (fn: (e: WSEvent) => void) => () => void
}

export function TaskList({ subscribe }: TaskListProps) {
  const { tasks, loading } = useTasks(subscribe)

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-zinc-100">Tasks</h1>
        <Link to="/tasks/new">
          <Button size="sm">New Task</Button>
        </Link>
      </div>
      <div className="rounded-lg border border-zinc-800 bg-zinc-900">
        <Table>
          <TableHeader>
            <TableRow className="border-zinc-800 hover:bg-transparent">
              <TableHead className="text-zinc-400">Status</TableHead>
              <TableHead className="text-zinc-400">Prompt</TableHead>
              <TableHead className="text-zinc-400">Repository</TableHead>
              <TableHead className="text-zinc-400">Source</TableHead>
              <TableHead className="text-zinc-400">Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && (
              <TableRow className="border-zinc-800">
                <TableCell colSpan={5} className="h-[72px] text-center text-zinc-500">
                  Loading tasks...
                </TableCell>
              </TableRow>
            )}
            {!loading && tasks.length === 0 && (
              <TableRow className="border-zinc-800">
                <TableCell colSpan={5} className="h-[72px] text-center text-zinc-500">
                  No tasks yet.{" "}
                  <Link to="/tasks/new" className="text-sky-400 hover:underline">
                    Create one
                  </Link>
                </TableCell>
              </TableRow>
            )}
            {tasks.map((task) => (
              <TableRow key={task.id} className="h-[72px] border-zinc-800">
                <TableCell>
                  <StatusBadge status={task.status} />
                </TableCell>
                <TableCell>
                  <Link
                    to={`/tasks/${task.id}`}
                    className="text-sm text-zinc-100 hover:text-sky-400"
                  >
                    {task.prompt.length > 80
                      ? task.prompt.slice(0, 80) + "..."
                      : task.prompt}
                  </Link>
                </TableCell>
                <TableCell className="text-sm text-zinc-400">
                  {task.repo_url.replace(/^https?:\/\/github\.com\//, "")}
                </TableCell>
                <TableCell className="text-sm text-zinc-400">
                  {task.source_type}
                </TableCell>
                <TableCell className="text-sm text-zinc-400">
                  <TimeAgo date={task.created_at} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
