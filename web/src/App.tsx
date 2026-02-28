import { BrowserRouter, Route, Routes } from "react-router-dom"
import { useWebSocket } from "@/hooks/useWebSocket"
import { Layout } from "@/components/Layout"
import { Dashboard } from "@/pages/Dashboard"
import { TaskList } from "@/pages/TaskList"
import { TaskDetail } from "@/pages/TaskDetail"
import { NewTask } from "@/pages/NewTask"

export function App() {
  const { subscribe } = useWebSocket()

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Dashboard subscribe={subscribe} />} />
          <Route path="tasks" element={<TaskList subscribe={subscribe} />} />
          <Route path="tasks/new" element={<NewTask />} />
          <Route path="tasks/:id" element={<TaskDetail subscribe={subscribe} />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
