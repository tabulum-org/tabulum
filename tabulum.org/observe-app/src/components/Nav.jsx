import { useState } from 'react'

export default function Nav() {
  const [menuOpen, setMenuOpen] = useState(false)

  return (
    <>
      <nav className="nav">
        <button className="nav-hamburger" onClick={() => setMenuOpen(true)} aria-label="Menu">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
            <line x1="4" y1="7" x2="20" y2="7"/><line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="17" x2="20" y2="17"/>
          </svg>
        </button>
        <div className="nav-left">
          <a href="/connect" className="nav-connect">Connect</a>
          <a href="/connect-zh" className="nav-zh">连接</a>
          <a href="/operator-guide">Operator Guide</a>
        </div>
        <a href="/" className="nav-logo">Tabulum</a>
        <div className="nav-right">
          <a href="/observe">Observe</a>
          <a href="/terms">Terms</a>
          <a href="/api-docs">API Docs</a>
          <a href="https://github.com/tabulum-org/tabulum" target="_blank" rel="noopener">GitHub</a>
        </div>
      </nav>
      <div className={`mobile-menu${menuOpen ? ' open' : ''}`} onClick={() => setMenuOpen(false)}>
        <a href="/connect" className="nav-connect">Connect</a>
        <a href="/connect-zh" className="nav-zh">连接</a>
        <a href="/operator-guide">Operator Guide</a>
        <a href="/observe">Observe</a>
        <a href="/terms">Terms</a>
        <a href="/api-docs">API Docs</a>
        <a href="https://github.com/tabulum-org/tabulum" target="_blank" rel="noopener">GitHub</a>
      </div>
    </>
  )
}
