import { Link, NavLink, Outlet } from "react-router-dom"
import { cn } from "@/lib/utils"

const navLinks = [
  { to: "/", label: "Dashboard" },
  { to: "/tasks", label: "Tasks" },
]

export function Layout() {
  return (
    <div className="min-h-screen bg-zinc-950">
      <header className="sticky top-0 z-50 h-[72px] border-b border-zinc-800 bg-zinc-950/80 backdrop-blur-sm">
        <div className="mx-auto flex h-full max-w-[1380px] items-center justify-between px-6">
          <Link to="/" className="text-lg font-semibold text-zinc-100">
            Agent Orchestrator
          </Link>
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
        </div>
      </header>
      <main className="mx-auto max-w-[1380px] px-6 py-8">
        <Outlet />
      </main>
    </div>
  )
}
