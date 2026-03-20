import { useEffect, useRef, useState, useCallback } from 'react'
import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide } from 'd3-force'
import { select } from 'd3-selection'
import { scaleLinear, scaleSqrt } from 'd3-scale'
import { zoom as d3Zoom, zoomIdentity } from 'd3-zoom'
import { drag as d3Drag } from 'd3-drag'
import usePolling from '../hooks/usePolling'

// Generate a deterministic hue from an address string
function hashHue(str) {
  let h = 0
  for (let i = 0; i < str.length; i++) {
    h = ((h << 5) - h + str.charCodeAt(i)) | 0
  }
  return Math.abs(h) % 360
}

// Assign colors to nodes so adjacent nodes are maximally different
function assignColors(nodes, edges) {
  const colors = new Map()
  const adjacency = new Map()

  for (const n of nodes) adjacency.set(n.id || n.address, new Set())
  for (const e of edges) {
    const src = typeof e.source === 'object' ? (e.source.id || e.source.address) : e.source
    const tgt = typeof e.target === 'object' ? (e.target.id || e.target.address) : e.target
    if (adjacency.has(src)) adjacency.get(src).add(tgt)
    if (adjacency.has(tgt)) adjacency.get(tgt).add(src)
  }

  const sorted = [...nodes].sort((a, b) => {
    const aId = a.id || a.address
    const bId = b.id || b.address
    return (adjacency.get(bId)?.size || 0) - (adjacency.get(aId)?.size || 0)
  })

  const goldenAngle = 137.508
  const baseHue = sorted.length > 0 ? hashHue(sorted[0].id || sorted[0].address) : 0

  for (let i = 0; i < sorted.length; i++) {
    const node = sorted[i]
    const nodeId = node.id || node.address
    const neighborHues = []
    for (const nid of adjacency.get(nodeId) || []) {
      if (colors.has(nid)) neighborHues.push(colors.get(nid))
    }

    let bestHue = (baseHue + i * goldenAngle) % 360

    if (neighborHues.length > 0) {
      let bestMinDist = -1
      for (let attempt = 0; attempt < 12; attempt++) {
        const candidate = (baseHue + (i + attempt * 3) * goldenAngle) % 360
        const minDist = Math.min(...neighborHues.map(nh => {
          const diff = Math.abs(candidate - nh)
          return Math.min(diff, 360 - diff)
        }))
        if (minDist > bestMinDist) {
          bestMinDist = minDist
          bestHue = candidate
        }
      }
    }

    colors.set(nodeId, bestHue)
  }

  return colors
}

function hueToColor(hue, alpha) {
  return `hsla(${Math.round(hue)}, 45%, 65%, ${alpha})`
}

function hueToHex(hue) {
  const s = 0.45, l = 0.65
  const c = (1 - Math.abs(2 * l - 1)) * s
  const x = c * (1 - Math.abs(((hue / 60) % 2) - 1))
  const m = l - c / 2
  let r, g, b
  if (hue < 60) { r = c; g = x; b = 0 }
  else if (hue < 120) { r = x; g = c; b = 0 }
  else if (hue < 180) { r = 0; g = c; b = x }
  else if (hue < 240) { r = 0; g = x; b = c }
  else if (hue < 300) { r = x; g = 0; b = c }
  else { r = c; g = 0; b = x }
  const toHex = (v) => Math.round((v + m) * 255).toString(16).padStart(2, '0')
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`
}

// 2D renderer
function render2D({ container, data, nodeColors }) {
  const svg = select(container)
  const width = container.clientWidth
  const height = container.clientHeight
  svg.selectAll('*').remove()
  const g = svg.append('g')

  const zoomBehavior = d3Zoom()
    .scaleExtent([0.3, 4])
    .on('zoom', (event) => g.attr('transform', event.transform))
  svg.call(zoomBehavior)

  const nodes = data.nodes.map(n => ({
    ...n, id: n.address,
    activity: (n.messages_sent || 0) + (n.messages_received || 0),
  }))
  const maxActivity = Math.max(1, ...nodes.map(n => n.activity))
  const maxEdgeCount = Math.max(1, ...data.edges.map(e => e.count || 1))
  const nodeRadius = scaleSqrt().domain([0, maxActivity]).range([8, 28])
  const edgeWidth = scaleLinear().domain([0, maxEdgeCount]).range([1, 6])
  const edgeOpacity = scaleLinear().domain([0, maxEdgeCount]).range([0.15, 0.5])

  const edges = data.edges.map(e => ({ source: e.from, target: e.to, count: e.count || 1 }))

  const sim = forceSimulation(nodes)
    .force('link', forceLink(edges).id(d => d.id).distance(120))
    .force('charge', forceManyBody().strength(-300))
    .force('center', forceCenter(width / 2, height / 2))
    .force('collide', forceCollide().radius(d => nodeRadius(d.activity) + 10))

  const link = g.selectAll('.link').data(edges).enter().append('line')
    .attr('stroke', d => {
      const srcId = typeof d.source === 'object' ? d.source.id : d.source
      return hueToColor(nodeColors.get(srcId) || 0, 0.25)
    })
    .attr('stroke-width', d => edgeWidth(d.count))
    .attr('stroke-opacity', d => edgeOpacity(d.count))

  const node = g.selectAll('.node').data(nodes).enter().append('circle')
    .attr('r', d => nodeRadius(d.activity))
    .attr('fill', d => hueToColor(nodeColors.get(d.id) || 0, 0.6))
    .attr('stroke', d => hueToColor(nodeColors.get(d.id) || 0, 0.3))
    .attr('stroke-width', 1.5)
    .style('cursor', 'grab')

  const dragBehavior = d3Drag()
    .on('start', (event, d) => { if (!event.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y })
    .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y })
    .on('end', (event, d) => { if (!event.active) sim.alphaTarget(0); d.fx = null; d.fy = null })
  node.call(dragBehavior)

  const label = g.selectAll('.label').data(nodes).enter().append('text')
    .text(d => d.address?.replace('tab_', '') || d.id)
    .attr('text-anchor', 'middle')
    .attr('dy', d => nodeRadius(d.activity) + 14)
    .attr('fill', 'rgba(232,230,225,0.4)')
    .attr('font-family', "'JetBrains Mono', monospace")
    .attr('font-size', '10px')

  sim.on('tick', () => {
    link.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y)
    node.attr('cx', d => d.x).attr('cy', d => d.y)
    label.attr('x', d => d.x).attr('y', d => d.y)
  })

  setTimeout(() => svg.call(zoomBehavior.transform, zoomIdentity), 100)

  return { destroy: () => sim.stop() }
}

// 3D renderer (lazy-loaded)
async function render3D({ container, data, nodeColors }) {
  const ForceGraph3D = (await import('3d-force-graph')).default

  // Clear container
  while (container.firstChild) container.removeChild(container.firstChild)

  const nodes = data.nodes.map(n => ({
    id: n.address,
    address: n.address,
    messages_sent: n.messages_sent || 0,
    messages_received: n.messages_received || 0,
    activity: (n.messages_sent || 0) + (n.messages_received || 0),
  }))

  const maxActivity = Math.max(1, ...nodes.map(n => n.activity))

  const edges = data.edges.map(e => ({
    source: e.from,
    target: e.to,
    count: e.count || 1,
  }))

  const maxEdgeCount = Math.max(1, ...edges.map(e => e.count))

  const graph = ForceGraph3D()(container)
    .backgroundColor('#00000000')
    .width(container.clientWidth)
    .height(container.clientHeight)
    .graphData({ nodes, links: edges })
    .nodeColor(n => hueToHex(nodeColors.get(n.id) || 0))
    .nodeVal(n => 1 + (n.activity / maxActivity) * 8)
    .nodeLabel(n => `${n.address}\nSent: ${n.messages_sent}  Received: ${n.messages_received}`)
    .nodeOpacity(0.85)
    .linkColor(l => {
      const srcId = typeof l.source === 'object' ? l.source.id : l.source
      return hueToHex(nodeColors.get(srcId) || 0) + '66'
    })
    .linkWidth(l => 0.5 + (l.count / maxEdgeCount) * 3)
    .linkOpacity(0.4)
    .linkDirectionalArrowLength(3)
    .linkDirectionalArrowRelPos(1)

  // Style the canvas background to transparent
  const renderer = graph.renderer()
  if (renderer) renderer.setClearColor(0x000000, 0)

  return {
    destroy: () => {
      graph._destructor && graph._destructor()
      while (container.firstChild) container.removeChild(container.firstChild)
    }
  }
}

export default function NetworkGraph() {
  const svgRef = useRef(null)
  const threeDRef = useRef(null)
  const tooltipRef = useRef(null)
  const instanceRef = useRef(null)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [mode, setMode] = useState('2d')

  const { data, loading, error } = usePolling('/observe/network', {
    interval: 30000,
    enabled: autoRefresh,
  })

  const renderGraph = useCallback(() => {
    if (!data?.nodes?.length) return

    // Clean up previous instance
    if (instanceRef.current) {
      instanceRef.current.destroy()
      instanceRef.current = null
    }

    const edges = data.edges.map(e => ({ source: e.from, target: e.to, count: e.count || 1 }))
    const nodeColors = assignColors(
      data.nodes.map(n => ({ id: n.address, ...n })),
      edges
    )

    if (mode === '2d' && svgRef.current) {
      instanceRef.current = render2D({ container: svgRef.current, data, nodeColors })
    } else if (mode === '3d' && threeDRef.current) {
      render3D({ container: threeDRef.current, data, nodeColors }).then(inst => {
        instanceRef.current = inst
      })
    }
  }, [data, mode])

  useEffect(() => {
    renderGraph()
    return () => {
      if (instanceRef.current) {
        instanceRef.current.destroy()
        instanceRef.current = null
      }
    }
  }, [renderGraph])

  useEffect(() => {
    const onResize = () => renderGraph()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [renderGraph])

  // Tooltip for 2D mode
  useEffect(() => {
    if (mode !== '2d' || !svgRef.current || !tooltipRef.current || !data?.nodes?.length) return
    const svg = select(svgRef.current)
    const tooltip = tooltipRef.current

    svg.selectAll('circle')
      .on('mouseenter', (event, d) => {
        tooltip.classList.add('visible')
        tooltip.querySelector('.graph-tooltip-address').textContent = d.address
        tooltip.querySelector('.graph-tooltip-stat').textContent =
          `Sent: ${d.messages_sent || 0}  |  Received: ${d.messages_received || 0}`
      })
      .on('mousemove', (event) => {
        const rect = svgRef.current.getBoundingClientRect()
        tooltip.style.left = `${event.clientX - rect.left + 12}px`
        tooltip.style.top = `${event.clientY - rect.top - 10}px`
      })
      .on('mouseleave', () => tooltip.classList.remove('visible'))
  }, [mode, data])

  return (
    <>
      <div className="controls">
        <button
          className={`control-btn${mode === '2d' ? ' active' : ''}`}
          onClick={() => setMode('2d')}
        >2D</button>
        <button
          className={`control-btn${mode === '3d' ? ' active' : ''}`}
          onClick={() => setMode('3d')}
        >3D</button>
        <div className="control-divider" />
        <label className="control-checkbox">
          <input type="checkbox" checked={autoRefresh} onChange={e => setAutoRefresh(e.target.checked)} />
          Auto-refresh (30s)
        </label>
        {loading && <span style={{ fontSize: '0.68rem', color: 'var(--text-faint)' }}>Loading...</span>}
        {error && <span style={{ fontSize: '0.68rem', color: 'var(--event-state-delete)' }}>Error: {error}</span>}
        {data?.nodes && (
          <span style={{ fontSize: '0.68rem', color: 'var(--text-faint)' }}>
            {data.nodes.length} agents, {data.edges.length} connections
          </span>
        )}
      </div>
      <div className="graph-container">
        {mode === '2d' && <svg ref={svgRef} />}
        {mode === '3d' && <div ref={threeDRef} style={{ width: '100%', height: '65vh' }} />}
        <div className="graph-tooltip" ref={tooltipRef}>
          <div className="graph-tooltip-address" />
          <div className="graph-tooltip-stat" />
        </div>
        {!data?.nodes?.length && !loading && (
          <div style={{
            position: 'absolute', inset: 0,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: 'var(--text-faint)', fontSize: '0.85rem',
          }}>
            No network data available
          </div>
        )}
      </div>
    </>
  )
}
