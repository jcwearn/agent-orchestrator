import { Navigate, Outlet } from "react-router-dom"
import type { AuthUser } from "@/types/api"

interface AuthGuardProps {
  loading: boolean
  setupRequired: boolean
  user: AuthUser | null
}

export function AuthGuard({ loading, setupRequired, user }: AuthGuardProps) {
  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-zinc-950">
        <div className="text-zinc-400">Loading...</div>
      </div>
    )
  }

  if (setupRequired) {
    return <Navigate to="/setup" replace />
  }

  if (!user) {
    return <Navigate to="/login" replace />
  }

  return <Outlet />
}
