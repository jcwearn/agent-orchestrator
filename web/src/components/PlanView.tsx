import Markdown from "react-markdown"
import remarkGfm from "remark-gfm"

interface PlanViewProps {
  plan: string | null
  revision: number
}

export function PlanView({ plan, revision }: PlanViewProps) {
  if (!plan) {
    return (
      <div className="rounded-lg border border-zinc-800 bg-zinc-900 p-6">
        <p className="text-sm text-zinc-500">No plan generated yet.</p>
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-900">
      <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-2">
        <span className="text-sm font-medium text-zinc-400">Plan</span>
        {revision > 0 && (
          <span className="text-xs text-zinc-500">
            Revision {revision}
          </span>
        )}
      </div>
      <div className="prose prose-invert prose-sm max-w-none p-4 prose-headings:text-zinc-200 prose-p:text-zinc-300 prose-strong:text-zinc-200 prose-code:text-sky-400 prose-pre:bg-zinc-950 prose-pre:border prose-pre:border-zinc-800 prose-li:text-zinc-300 prose-a:text-sky-400">
        <Markdown remarkPlugins={[remarkGfm]}>{plan}</Markdown>
      </div>
    </div>
  )
}
