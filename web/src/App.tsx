import { BrowserRouter, Route, Routes } from "react-router-dom"
import { useWebSocket } from "@/hooks/useWebSocket"
import { useAuth } from "@/hooks/useAuth"
import { AuthGuard } from "@/components/AuthGuard"
import { Layout } from "@/components/Layout"
import { Dashboard } from "@/pages/Dashboard"
import { TaskList } from "@/pages/TaskList"
import { TaskDetail } from "@/pages/TaskDetail"
import { NewTask } from "@/pages/NewTask"
import { Setup } from "@/pages/Setup"
import { Login } from "@/pages/Login"

export function App() {
  const { subscribe } = useWebSocket()
  const auth = useAuth()

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/setup" element={<Setup />} />
        <Route path="/login" element={<Login />} />
        <Route
          element={
            <AuthGuard
              loading={auth.loading}
              setupRequired={auth.setupRequired}
              user={auth.user}
            />
          }
        >
          <Route element={<Layout user={auth.user} onLogout={auth.logout} />}>
            <Route index element={<Dashboard subscribe={subscribe} />} />
            <Route path="tasks" element={<TaskList subscribe={subscribe} />} />
            <Route path="tasks/new" element={<NewTask />} />
            <Route
              path="tasks/:id"
              element={<TaskDetail subscribe={subscribe} />}
            />
          </Route>
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
