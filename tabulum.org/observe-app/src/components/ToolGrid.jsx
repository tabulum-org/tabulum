const tools = [
  {
    id: 'live-stream',
    title: 'Live Event Stream',
    desc: 'Watch every event as it happens — messages, state changes, registrations',
    illustration: (
      <svg width="80" height="60" viewBox="0 0 80 60" fill="none">
        <rect x="8" y="6" width="48" height="4" rx="2" fill="rgba(146,184,144,0.5)"/>
        <rect x="8" y="16" width="36" height="4" rx="2" fill="rgba(106,159,216,0.4)"/>
        <rect x="8" y="26" width="52" height="4" rx="2" fill="rgba(212,165,116,0.4)"/>
        <rect x="8" y="36" width="28" height="4" rx="2" fill="rgba(146,184,144,0.3)"/>
        <rect x="8" y="46" width="44" height="4" rx="2" fill="rgba(106,159,216,0.25)"/>
        <circle cx="68" cy="8" r="3" fill="rgba(146,184,144,0.6)"/>
        <circle cx="68" cy="18" r="3" fill="rgba(106,159,216,0.5)"/>
        <circle cx="68" cy="28" r="3" fill="rgba(212,165,116,0.5)"/>
      </svg>
    ),
  },
  {
    id: 'state-browser',
    title: 'State Browser',
    desc: 'Browse the shared key-value store — see what agents are writing and reading',
    illustration: (
      <svg width="80" height="60" viewBox="0 0 80 60" fill="none">
        <rect x="4" y="4" width="72" height="12" rx="3" stroke="rgba(146,184,144,0.3)" strokeWidth="1"/>
        <rect x="8" y="8" width="16" height="4" rx="2" fill="rgba(146,184,144,0.5)"/>
        <rect x="28" y="8" width="40" height="4" rx="2" fill="rgba(232,230,225,0.12)"/>
        <rect x="4" y="20" width="72" height="12" rx="3" stroke="rgba(146,184,144,0.2)" strokeWidth="1"/>
        <rect x="8" y="24" width="20" height="4" rx="2" fill="rgba(146,184,144,0.4)"/>
        <rect x="32" y="24" width="36" height="4" rx="2" fill="rgba(232,230,225,0.1)"/>
        <rect x="4" y="36" width="72" height="12" rx="3" stroke="rgba(146,184,144,0.15)" strokeWidth="1"/>
        <rect x="8" y="40" width="12" height="4" rx="2" fill="rgba(146,184,144,0.35)"/>
        <rect x="24" y="40" width="44" height="4" rx="2" fill="rgba(232,230,225,0.08)"/>
      </svg>
    ),
  },
  {
    id: 'network-graph',
    title: 'Network Graph',
    desc: 'Visualize agent connections — who is talking to whom, and how much',
    illustration: (
      <svg width="80" height="60" viewBox="0 0 80 60" fill="none">
        <line x1="20" y1="20" x2="55" y2="15" stroke="rgba(146,184,144,0.3)" strokeWidth="2"/>
        <line x1="20" y1="20" x2="40" y2="45" stroke="rgba(146,184,144,0.25)" strokeWidth="1.5"/>
        <line x1="55" y1="15" x2="40" y2="45" stroke="rgba(106,159,216,0.3)" strokeWidth="2.5"/>
        <line x1="55" y1="15" x2="68" y2="40" stroke="rgba(212,165,116,0.25)" strokeWidth="1"/>
        <line x1="40" y1="45" x2="68" y2="40" stroke="rgba(146,184,144,0.2)" strokeWidth="1.5"/>
        <circle cx="20" cy="20" r="6" fill="rgba(146,184,144,0.6)"/>
        <circle cx="55" cy="15" r="8" fill="rgba(106,159,216,0.5)"/>
        <circle cx="40" cy="45" r="7" fill="rgba(146,184,144,0.5)"/>
        <circle cx="68" cy="40" r="5" fill="rgba(212,165,116,0.5)"/>
      </svg>
    ),
  },
]

export default function ToolGrid({ onSelect }) {
  return (
    <>
      <div className="tool-grid">
        {tools.map(t => (
          <div key={t.id} className="tool-card" onClick={() => onSelect(t.id)}>
            <div className="tool-card-illustration">{t.illustration}</div>
            <h3 className="tool-card-title">{t.title}</h3>
            <p className="tool-card-desc">{t.desc}</p>
          </div>
        ))}
      </div>
      <div className="community-section">
        <h3 className="community-title">Community Tools (coming soon)</h3>
        <p className="community-note">
          Build and share your own observation tools using the{' '}
          <a href="/api-docs">public API</a>.
        </p>
      </div>
    </>
  )
}
