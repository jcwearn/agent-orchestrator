import { useState, useMemo } from "react"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { approveTask, sendFeedback } from "@/api/client"

interface DecisionGroup {
  title: string
  options: { label: string; description: string }[]
}

function parseDecisions(plan: string | null): DecisionGroup[] {
  if (!plan) return []
  const groups: DecisionGroup[] = []
  let current: DecisionGroup | null = null

  for (const line of plan.split("\n")) {
    const trimmed = line.trim()
    const headingMatch = trimmed.match(/^###\s+Decision:\s+(.+)/)
    if (headingMatch) {
      current = { title: headingMatch[1], options: [] }
      groups.push(current)
      continue
    }
    if (current) {
      const optMatch = trimmed.match(/^- \[ \]\s+(.+?)(?:\s+--\s+(.+))?$/)
      if (optMatch) {
        current.options.push({
          label: optMatch[1],
          description: optMatch[2] ?? "",
        })
      } else if (trimmed !== "" && !trimmed.startsWith("- [ ]")) {
        current = null
      }
    }
  }

  return groups.filter((g) => g.options.length > 0)
}

function buildDecisionsString(
  groups: DecisionGroup[],
  selections: Record<string, string>,
): string {
  const lines: string[] = []
  for (const group of groups) {
    const selected = selections[group.title]
    if (selected) {
      const opt = group.options.find((o) => o.label === selected)
      if (opt) {
        const suffix = opt.description ? ` -- ${opt.description}` : ""
        lines.push(`- [x] ${opt.label}${suffix}`)
      }
    }
  }
  return lines.join("\n")
}

interface ApprovalFormProps {
  taskId: string
  plan: string | null
  onAction: () => void
}

export function ApprovalForm({ taskId, plan, onAction }: ApprovalFormProps) {
  const [feedback, setFeedback] = useState("")
  const [loading, setLoading] = useState(false)
  const [runTests, setRunTests] = useState(false)
  const [selections, setSelections] = useState<Record<string, string>>({})

  const decisions = useMemo(() => parseDecisions(plan), [plan])

  function selectOption(title: string, label: string) {
    setSelections((prev) => ({ ...prev, [title]: label }))
  }

  async function handleApprove() {
    setLoading(true)
    try {
      const decisionsStr = buildDecisionsString(decisions, selections)
      await approveTask(taskId, {
        run_tests: runTests,
        decisions: decisionsStr || undefined,
      })
      onAction()
    } finally {
      setLoading(false)
    }
  }

  async function handleFeedback() {
    if (!feedback.trim()) return
    setLoading(true)
    try {
      const decisionsStr = buildDecisionsString(decisions, selections)
      await sendFeedback(taskId, feedback, decisionsStr || undefined)
      setFeedback("")
      onAction()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4">
      <p className="mb-3 text-sm font-medium text-amber-400">
        This task is awaiting your approval.
      </p>

      {decisions.length > 0 && (
        <div className="mb-4 space-y-4">
          {decisions.map((group) => (
            <fieldset key={group.title}>
              <legend className="mb-2 text-sm font-medium text-zinc-200">
                {group.title}
              </legend>
              <div className="space-y-1.5">
                {group.options.map((opt) => (
                  <label
                    key={opt.label}
                    className="flex items-start gap-2 cursor-pointer"
                  >
                    <input
                      type="radio"
                      name={`decision-${group.title}`}
                      checked={selections[group.title] === opt.label}
                      onChange={() => selectOption(group.title, opt.label)}
                      className="mt-1 accent-amber-500"
                    />
                    <span className="text-sm text-zinc-300">
                      {opt.label}
                      {opt.description && (
                        <span className="text-zinc-500">
                          {" "}
                          — {opt.description}
                        </span>
                      )}
                    </span>
                  </label>
                ))}
              </div>
            </fieldset>
          ))}
        </div>
      )}

      <label className="mb-4 flex items-center gap-2 cursor-pointer">
        <input
          type="checkbox"
          checked={runTests}
          onChange={(e) => setRunTests(e.target.checked)}
          className="accent-amber-500"
        />
        <span className="text-sm text-zinc-300">
          Run tests before creating PR
        </span>
      </label>

      <div className="flex gap-3">
        <Button onClick={handleApprove} disabled={loading} size="sm">
          Approve Plan
        </Button>
      </div>
      <div className="mt-4">
        <Textarea
          placeholder="Or provide feedback for revision..."
          value={feedback}
          onChange={(e) => setFeedback(e.target.value)}
          className="mb-2 bg-zinc-950 border-zinc-700"
          rows={3}
        />
        <Button
          onClick={handleFeedback}
          disabled={loading || !feedback.trim()}
          variant="outline"
          size="sm"
        >
          Send Feedback
        </Button>
      </div>
    </div>
  )
}
