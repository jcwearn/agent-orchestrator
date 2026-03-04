import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
interface SetupProps {
  onSetup: (username: string, password: string) => Promise<void>
}

export function Setup({ onSetup }: SetupProps) {
  const navigate = useNavigate()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError("")

    if (password !== confirmPassword) {
      setError("Passwords do not match")
      return
    }
    if (password.length < 8) {
      setError("Password must be at least 8 characters")
      return
    }

    setLoading(true)
    try {
      await onSetup(username, password)
      navigate("/")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Setup failed")
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <Card className="w-full max-w-md border-zinc-800 bg-zinc-900">
        <CardHeader>
          <CardTitle className="text-zinc-100">Create Admin Account</CardTitle>
          <CardDescription className="text-zinc-400">
            Set up the initial administrator account for Agent Orchestrator.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">Username</Label>
              <Input
                id="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="bg-zinc-950 border-zinc-700"
                autoComplete="username"
                required
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="bg-zinc-950 border-zinc-700"
                autoComplete="new-password"
                required
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="confirm_password">Confirm Password</Label>
              <Input
                id="confirm_password"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                className="bg-zinc-950 border-zinc-700"
                autoComplete="new-password"
                required
              />
            </div>

            {error && <p className="text-sm text-red-400">{error}</p>}

            <Button
              type="submit"
              className="w-full"
              disabled={loading || !username || !password || !confirmPassword}
            >
              {loading ? "Creating account..." : "Create Account"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
