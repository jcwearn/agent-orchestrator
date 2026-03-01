import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { approveTask, sendFeedback } from "@/api/client"

interface ApprovalFormProps {
  taskId: string
  onAction: () => void
}

export function ApprovalForm({ taskId, onAction }: ApprovalFormProps) {
  const [feedback, setFeedback] = useState("")
  const [loading, setLoading] = useState(false)

  async function handleApprove() {
    setLoading(true)
    try {
      await approveTask(taskId)
      onAction()
    } finally {
      setLoading(false)
    }
  }

  async function handleFeedback() {
    if (!feedback.trim()) return
    setLoading(true)
    try {
      await sendFeedback(taskId, feedback)
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
