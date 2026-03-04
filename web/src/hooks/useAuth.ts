import { useCallback, useEffect, useState } from "react"
import type { AuthUser } from "@/types/api"
import * as api from "@/api/client"

interface AuthState {
  user: AuthUser | null
  loading: boolean
  setupRequired: boolean
}

export function useAuth() {
  const [state, setState] = useState<AuthState>({
    user: null,
    loading: true,
    setupRequired: false,
  })

  const checkAuth = useCallback(async () => {
    try {
      const status = await api.getAuthStatus()
      setState({
        user: status.user,
        loading: false,
        setupRequired: status.setup_required,
      })
    } catch {
      setState({ user: null, loading: false, setupRequired: false })
    }
  }, [])

  useEffect(() => {
    checkAuth()
  }, [checkAuth])

  const login = useCallback(async (username: string, password: string) => {
    const status = await api.login({ username, password })
    setState({
      user: status.user,
      loading: false,
      setupRequired: false,
    })
  }, [])

  const logout = useCallback(async () => {
    await api.logout()
    setState({ user: null, loading: false, setupRequired: false })
  }, [])

  const setup = useCallback(async (username: string, password: string) => {
    const status = await api.setup({ username, password })
    setState({
      user: status.user,
      loading: false,
      setupRequired: false,
    })
  }, [])

  return {
    user: state.user,
    loading: state.loading,
    setupRequired: state.setupRequired,
    login,
    logout,
    setup,
  }
}
