import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

const statusConfig: Record<string, { label: string; className: string }> = {
  queued: {
    label: "Queued",
    className: "bg-zinc-700 text-zinc-300 hover:bg-zinc-700",
  },
  planning: {
    label: "Planning",
    className: "bg-blue-500/20 text-blue-400 hover:bg-blue-500/20",
  },
  awaiting_approval: {
    label: "Awaiting Approval",
    className: "bg-amber-500/20 text-amber-400 hover:bg-amber-500/20",
  },
  implementing: {
    label: "Implementing",
    className: "bg-blue-500/20 text-blue-400 hover:bg-blue-500/20",
  },
  complete: {
    label: "Complete",
    className: "bg-green-500/20 text-green-400 hover:bg-green-500/20",
  },
  failed: {
    label: "Failed",
    className: "bg-red-500/20 text-red-400 hover:bg-red-500/20",
  },
  stopped: {
    label: "Stopped",
    className: "bg-zinc-700 text-zinc-300 hover:bg-zinc-700",
  },
}

export function StatusBadge({ status }: { status: string }) {
  const config = statusConfig[status] ?? {
    label: status,
    className: "bg-zinc-700 text-zinc-300 hover:bg-zinc-700",
  }
  return (
    <Badge variant="secondary" className={cn("font-medium", config.className)}>
      {config.label}
    </Badge>
  )
}
