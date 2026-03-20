import { useState, useEffect, useRef, useCallback } from 'react'

const WS_BASE = 'wss://observe.tabulum.org'
const POLL_BASE = 'https://observe.tabulum.org'

export default function useWebSocket(path, { filters = {}, paused = false } = {}) {
  const [events, setEvents] = useState([])
  const [status, setStatus] = useState('connecting') // connected | disconnected | connecting | polling
  const wsRef = useRef(null)
  const pausedRef = useRef(paused)
  const pollCursorRef = useRef(0)
  const pollIntervalRef = useRef(null)
  const fallbackRef = useRef(false)

  pausedRef.current = paused

  const buildUrl = useCallback(() => {
    const params = new URLSearchParams()
    if (filters.types?.length) params.set('type', filters.types.join(','))
    if (filters.agent) params.set('agent', filters.agent)
    const qs = params.toString()
    return `${WS_BASE}${path}${qs ? '?' + qs : ''}`
  }, [path, filters.types?.join(','), filters.agent])

  const startPolling = useCallback(() => {
    if (pollIntervalRef.current) return
    fallbackRef.current = true
    setStatus('polling')

    pollIntervalRef.current = setInterval(async () => {
      if (pausedRef.current) return
      try {
        const params = new URLSearchParams()
        params.set('since', String(pollCursorRef.current))
        params.set('limit', '100')
        if (filters.types?.length) params.set('type', filters.types.join(','))
        if (filters.agent) params.set('agent', filters.agent)

        const resp = await fetch(`${POLL_BASE}/observe/events?${params}`)
        if (!resp.ok) return
        const data = await resp.json()
        if (data.events?.length) {
          setEvents(prev => [...data.events.reverse(), ...prev].slice(0, 500))
          pollCursorRef.current = data.next_cursor || pollCursorRef.current
        }
      } catch {}
    }, 2000)
  }, [filters.types?.join(','), filters.agent])

  useEffect(() => {
    let reconnectTimer
    let ws

    function connect() {
      if (fallbackRef.current) return
      setStatus('connecting')

      try {
        ws = new WebSocket(buildUrl())
        wsRef.current = ws
      } catch {
        startPolling()
        return
      }

      ws.onopen = () => setStatus('connected')

      ws.onmessage = (e) => {
        if (pausedRef.current) return
        try {
          const event = JSON.parse(e.data)
          setEvents(prev => [event, ...prev].slice(0, 500))
        } catch {}
      }

      ws.onclose = () => {
        setStatus('disconnected')
        reconnectTimer = setTimeout(() => {
          if (!fallbackRef.current) connect()
        }, 3000)
      }

      ws.onerror = () => {
        ws.close()
        startPolling()
      }
    }

    connect()

    return () => {
      clearTimeout(reconnectTimer)
      clearInterval(pollIntervalRef.current)
      pollIntervalRef.current = null
      if (wsRef.current) {
        wsRef.current.onclose = null
        wsRef.current.close()
      }
    }
  }, [buildUrl, startPolling])

  const clear = useCallback(() => setEvents([]), [])

  return { events, status, clear }
}
