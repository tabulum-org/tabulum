import { useState, useEffect, useCallback } from 'react'
import Nav from './components/Nav'
import BackgroundCanvas from './components/BackgroundCanvas'
import ToolGrid from './components/ToolGrid'
import LiveEventStream from './components/LiveEventStream'
import StateBrowser from './components/StateBrowser'
import NetworkGraph from './components/NetworkGraph'

const TOOLS = {
  'live-stream': { component: LiveEventStream, title: 'Live Event Stream' },
  'state-browser': { component: StateBrowser, title: 'State Browser' },
  'network-graph': { component: NetworkGraph, title: 'Network Graph' },
}

function getToolFromPath() {
  const path = window.location.pathname.replace(/^\/observe\/?/, '').replace(/\/$/, '')
  return path && TOOLS[path] ? path : null
}

export default function App() {
  const [activeTool, setActiveTool] = useState(getToolFromPath)

  useEffect(() => {
    const onPopState = () => setActiveTool(getToolFromPath())
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  const navigateTo = useCallback((toolId) => {
    if (toolId) {
      window.history.pushState(null, '', `/observe/${toolId}`)
    } else {
      window.history.pushState(null, '', '/observe')
    }
    setActiveTool(toolId)
    window.scrollTo(0, 0)
  }, [])

  const tool = activeTool ? TOOLS[activeTool] : null
  const ToolComponent = tool?.component

  return (
    <>
      <BackgroundCanvas />
      <Nav />
      <main className="page">
        {!activeTool && (
          <>
            <div className="page-header">
              <h1 className="page-title">Observe</h1>
              <p className="page-subtitle">Watch the Tabulum ecosystem in real time</p>
            </div>
            <ToolGrid onSelect={navigateTo} />
          </>
        )}
        {activeTool && ToolComponent && (
          <div className="tool-panel">
            <button className="back-button" onClick={() => navigateTo(null)}>
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                <path d="M10 12L6 8L10 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
              All tools
            </button>
            <h2 className="tool-title">{tool.title}</h2>
            <ToolComponent />
          </div>
        )}
      </main>
      <footer className="footer">
        <p>Tabulum — the surface</p>
      </footer>
    </>
  )
}
