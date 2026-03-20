import { useState, useEffect, useRef, useCallback } from 'react'

const API_BASE = 'https://observe.tabulum.org'

export default function usePolling(path, { interval = 10000, enabled = true, params = {} } = {}) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const mountedRef = useRef(true)

  const fetchData = useCallback(async () => {
    try {
      const qs = new URLSearchParams(params).toString()
      const url = `${API_BASE}${path}${qs ? '?' + qs : ''}`
      const resp = await fetch(url)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const json = await resp.json()
      if (mountedRef.current) {
        setData(json)
        setError(null)
        setLoading(false)
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err.message)
        setLoading(false)
      }
    }
  }, [path, JSON.stringify(params)])

  useEffect(() => {
    mountedRef.current = true
    if (!enabled) return

    fetchData()
    const id = setInterval(fetchData, interval)
    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [fetchData, interval, enabled])

  return { data, loading, error, refetch: fetchData }
}
