import { Link, NavLink, Outlet, useNavigate } from "react-router-dom"
import { cn } from "@/lib/utils"
import type { AuthUser } from "@/types/api"

const navLinks = [
  { to: "/", label: "Dashboard" },
  { to: "/tasks", label: "Tasks" },
]

interface LayoutProps {
  user: AuthUser | null
  onLogout: () => Promise<void>
}

export function Layout({ user, onLogout }: LayoutProps) {
  const navigate = useNavigate()

  async function handleLogout() {
    await onLogout()
    navigate("/login")
  }

  return (
    <div className="min-h-screen bg-zinc-950">
      <header className="sticky top-0 z-50 h-[72px] border-b border-zinc-800 bg-zinc-950/80 backdrop-blur-sm">
        <div className="mx-auto flex h-full max-w-[1380px] items-center justify-between px-6">
          <Link to="/" className="text-lg font-semibold text-zinc-100">
            Agent Orchestrator
          </Link>
          <div className="flex items-center gap-4">
            <nav className="flex items-center gap-1">
              {navLinks.map((link) => (
                <NavLink
                  key={link.to}
                  to={link.to}
                  end={link.to === "/"}
                  className={({ isActive }) =>
                    cn(
                      "rounded-md px-3 py-2 text-sm font-medium transition-colors",
                      isActive
                        ? "bg-zinc-800 text-zinc-100"
                        : "text-zinc-400 hover:text-zinc-100",
                    )
                  }
                >
                  {link.label}
                </NavLink>
              ))}
            </nav>
            {user && (
              <div className="flex items-center gap-3 border-l border-zinc-800 pl-4">
                <span className="text-sm text-zinc-400">{user.username}</span>
                <button
                  onClick={handleLogout}
                  className="rounded-md px-2 py-1 text-sm text-zinc-400 transition-colors hover:text-zinc-100"
                >
                  Sign out
                </button>
              </div>
            )}
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-[1380px] px-6 py-8">
        <Outlet />
      </main>
    </div>
  )
}
