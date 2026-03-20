import { useState, useRef, useEffect } from 'react'
import useWebSocket from '../hooks/useWebSocket'

const EVENT_TYPES = [
  'agent_registered',
  'message_sent',
  'state_written',
  'state_deleted',
]

const EVENT_COLORS = {
  agent_registered: 'var(--event-register)',
  message_sent: 'var(--event-message)',
  state_written: 'var(--event-state-write)',
  state_deleted: 'var(--event-state-delete)',
}

function formatTime(ts) {
  try {
    const d = new Date(ts)
    return d.toLocaleTimeString(undefined, { hour12: false, fractionalSecondDigits: 3 })
  } catch {
    return ts
  }
}

function formatEventData(event) {
  const d = event.data
  if (!d) return ''
  switch (event.type) {
    case 'message_sent':
      return d.to ? `→ ${d.to}` : ''
    case 'state_written':
      return d.key || ''
    case 'state_deleted':
      return d.key || ''
    case 'agent_registered':
      return 'registered'
    default:
      return JSON.stringify(d).slice(0, 120)
  }
}

export default function LiveEventStream() {
  const [paused, setPaused] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [typeFilters, setTypeFilters] = useState(new Set(EVENT_TYPES))
  const [agentFilter, setAgentFilter] = useState('')
  const feedRef = useRef(null)

  const { events, status, clear } = useWebSocket('/observe/events/stream', {
    filters: {
      types: typeFilters.size === EVENT_TYPES.length ? [] : [...typeFilters],
      agent: agentFilter || undefined,
    },
    paused,
  })

  useEffect(() => {
    if (autoScroll && feedRef.current) {
      feedRef.current.scrollTop = 0
    }
  }, [events.length, autoScroll])

  function toggleType(type) {
    setTypeFilters(prev => {
      const next = new Set(prev)
      if (next.has(type)) next.delete(type)
      else next.add(type)
      return next
    })
  }

  return (
    <>
      <div className="controls">
        <span className="connection-status">
          <span className={`status-dot ${status === 'connected' || status === 'polling' ? 'connected' : status === 'connecting' ? 'connecting' : 'disconnected'}`} />
          {status}
        </span>
        <div className="control-divider" />
        <span className="control-label">Filter:</span>
        {EVENT_TYPES.map(t => (
          <label key={t} className="control-checkbox">
            <input type="checkbox" checked={typeFilters.has(t)} onChange={() => toggleType(t)} />
            <span style={{ color: EVENT_COLORS[t] }}>{t.replace('_', ' ')}</span>
          </label>
        ))}
        <div className="control-divider" />
        <input
          className="control-input"
          placeholder="agent address..."
          value={agentFilter}
          onChange={e => setAgentFilter(e.target.value)}
          style={{ width: '140px' }}
        />
        <div className="control-divider" />
        <button className={`control-btn${paused ? ' paused' : ''}`} onClick={() => setPaused(p => !p)}>
          {paused ? 'Resume' : 'Pause'}
        </button>
        <label className="control-checkbox">
          <input type="checkbox" checked={autoScroll} onChange={e => setAutoScroll(e.target.checked)} />
          Auto-scroll
        </label>
        <button className="control-btn" onClick={clear}>Clear</button>
      </div>
      <div className="event-feed">
        <div className="event-feed-inner" ref={feedRef}>
          {events.length === 0 && (
            <div className="event-empty">
              {status === 'connecting' ? 'Connecting...' : 'Waiting for events...'}
            </div>
          )}
          {events.map((ev, i) => (
            <div key={ev.seq || i} className="event-row">
              <span className="event-time">{formatTime(ev.ts)}</span>
              <span className="event-type" style={{ color: EVENT_COLORS[ev.type] || 'var(--event-default)' }}>
                {ev.type?.replace(/_/g, ' ')}
              </span>
              <span className="event-agent">{ev.agent || '—'}</span>
              <span className="event-data">{formatEventData(ev)}</span>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}
