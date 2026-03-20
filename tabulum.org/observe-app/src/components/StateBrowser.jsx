import { useState, useCallback } from 'react'
import usePolling from '../hooks/usePolling'

const API_BASE = 'https://observe.tabulum.org'

function formatTime(ts) {
  try {
    return new Date(ts).toLocaleString(undefined, {
      dateStyle: 'short',
      timeStyle: 'medium',
    })
  } catch {
    return ts
  }
}

function formatBytes(n) {
  if (n == null) return '—'
  if (n < 1024) return `${n} B`
  return `${(n / 1024).toFixed(1)} KB`
}

export default function StateBrowser() {
  const [prefix, setPrefix] = useState('')
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [expandedKey, setExpandedKey] = useState(null)
  const [expandedValue, setExpandedValue] = useState(null)
  const [loadingValue, setLoadingValue] = useState(false)

  const params = prefix ? { prefix, limit: '100' } : { limit: '100' }
  const { data, loading, error } = usePolling('/observe/state', {
    interval: 10000,
    enabled: autoRefresh,
    params,
  })

  const keys = data?.keys || []

  const fetchValue = useCallback(async (key) => {
    if (expandedKey === key) {
      setExpandedKey(null)
      setExpandedValue(null)
      return
    }
    setExpandedKey(key)
    setLoadingValue(true)
    try {
      const resp = await fetch(`${API_BASE}/observe/state/${encodeURIComponent(key)}`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const json = await resp.json()
      setExpandedValue(json)
    } catch (err) {
      setExpandedValue({ error: err.message })
    }
    setLoadingValue(false)
  }, [expandedKey])

  return (
    <>
      <div className="controls">
        <span className="control-label">Search:</span>
        <input
          className="control-input"
          placeholder="key prefix..."
          value={prefix}
          onChange={e => setPrefix(e.target.value)}
          style={{ width: '200px' }}
        />
        <div className="control-divider" />
        <label className="control-checkbox">
          <input type="checkbox" checked={autoRefresh} onChange={e => setAutoRefresh(e.target.checked)} />
          Auto-refresh (10s)
        </label>
        <div className="control-divider" />
        <span style={{ fontSize: '0.68rem', color: 'var(--text-faint)' }}>
          {data?.total != null ? `${data.total} keys` : loading ? 'Loading...' : ''}
        </span>
        {error && <span style={{ fontSize: '0.68rem', color: 'var(--event-state-delete)' }}>Error: {error}</span>}
      </div>
      <div className="state-table">
        <div className="state-header">
          <span>Key</span>
          <span>Modified by</span>
          <span>Modified at</span>
          <span style={{ textAlign: 'right' }}>Size</span>
        </div>
        {keys.length === 0 && !loading && (
          <div className="event-empty">No state keys found</div>
        )}
        {loading && keys.length === 0 && (
          <div className="event-empty">Loading...</div>
        )}
        {keys.map(k => (
          <div key={k.key}>
            <div className="state-row" onClick={() => fetchValue(k.key)}>
              <span className="state-key">{k.key}</span>
              <span className="state-agent">{k.last_modified_by || '—'}</span>
              <span className="state-time">{k.last_modified_at ? formatTime(k.last_modified_at) : '—'}</span>
              <span className="state-size">{formatBytes(k.value_length)}</span>
            </div>
            {expandedKey === k.key && (
              <div className="state-value-panel">
                {loadingValue ? (
                  <span style={{ color: 'var(--text-faint)', fontSize: '0.75rem' }}>Loading...</span>
                ) : expandedValue?.redacted ? (
                  <span className="state-redacted">{expandedValue.value}</span>
                ) : expandedValue?.error ? (
                  <span style={{ color: 'var(--event-state-delete)', fontSize: '0.75rem' }}>Error: {expandedValue.error}</span>
                ) : (
                  <pre className="state-value-content">
                    {typeof expandedValue?.value === 'string'
                      ? expandedValue.value
                      : JSON.stringify(expandedValue?.value, null, 2)}
                  </pre>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </>
  )
}
