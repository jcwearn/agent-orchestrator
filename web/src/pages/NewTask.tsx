import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Combobox } from "@/components/ui/combobox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { createTask, getConfig, listRepositories } from "@/api/client"
import type { RepoInfo } from "@/types/api"

export function NewTask() {
  const navigate = useNavigate()
  const [prompt, setPrompt] = useState("")
  const [repoUrl, setRepoUrl] = useState("")
  const [baseBranch, setBaseBranch] = useState("main")
  const [createIssue, setCreateIssue] = useState(false)
  const [repos, setRepos] = useState<RepoInfo[]>([])
  const [reposLoading, setReposLoading] = useState(false)
  const [githubConfigured, setGithubConfigured] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    getConfig().then((c) => setGithubConfigured(c.github_configured)).catch(() => {})
    setReposLoading(true)
    listRepositories()
      .then(setRepos)
      .catch(() => {})
      .finally(() => setReposLoading(false))
  }, [])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError("")
    setLoading(true)

    try {
      const task = await createTask({
        prompt,
        repo_url: repoUrl,
        base_branch: baseBranch,
        ...(createIssue && { create_issue: true }),
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
          <Combobox
            id="repo_url"
            options={repos.map((r) => ({ label: r.full_name, value: r.clone_url }))}
            value={repoUrl}
            onChange={setRepoUrl}
            placeholder="https://github.com/owner/repo"
            loading={reposLoading}
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

        {githubConfigured && (
          <div className="flex items-center gap-2">
            <input
              id="create_issue"
              type="checkbox"
              checked={createIssue}
              onChange={(e) => setCreateIssue(e.target.checked)}
              className="h-4 w-4 rounded border-zinc-700 bg-zinc-950"
            />
            <Label htmlFor="create_issue">Create GitHub issue</Label>
          </div>
        )}

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
