import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { createTask } from "@/api/client"

export function NewTask() {
  const navigate = useNavigate()
  const [prompt, setPrompt] = useState("")
  const [repoUrl, setRepoUrl] = useState("")
  const [baseBranch, setBaseBranch] = useState("main")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError("")
    setLoading(true)

    try {
      const task = await createTask({
        prompt,
        repo_url: repoUrl,
        base_branch: baseBranch,
      })
      navigate(`/tasks/${task.id}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create task")
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <h1 className="mb-6 text-2xl font-semibold text-zinc-100">New Task</h1>
      <form onSubmit={handleSubmit} className="space-y-6">
        <div className="space-y-2">
          <Label htmlFor="prompt">Prompt</Label>
          <Textarea
            id="prompt"
            placeholder="Describe the task for the agent..."
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            className="min-h-[120px] bg-zinc-950 border-zinc-700"
            required
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="repo_url">Repository URL</Label>
          <Input
            id="repo_url"
            placeholder="https://github.com/owner/repo"
            value={repoUrl}
            onChange={(e) => setRepoUrl(e.target.value)}
            className="bg-zinc-950 border-zinc-700"
            required
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="base_branch">Base Branch</Label>
          <Input
            id="base_branch"
            value={baseBranch}
            onChange={(e) => setBaseBranch(e.target.value)}
            className="bg-zinc-950 border-zinc-700"
          />
        </div>

        {error && (
          <p className="text-sm text-red-400">{error}</p>
        )}

        <div className="flex gap-3">
          <Button type="submit" disabled={loading || !prompt || !repoUrl}>
            {loading ? "Creating..." : "Create Task"}
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => navigate("/tasks")}
          >
            Cancel
          </Button>
        </div>
      </form>
    </div>
  )
}
