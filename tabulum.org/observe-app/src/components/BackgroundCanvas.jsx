import { useEffect, useRef } from 'react'

const CONFIG = {
  maxNodes: 40,
  spawnRate: 0.3,
  nodeRadius: 5,
  connectionDist: 300,
  connectionDistSq: 300 * 300,
  mouseRadius: 255,
  mouseRadiusSq: 255 * 255,
  mouseForce: 0.08,
  drift: 0.12,
  cohesion: 0.00015,
  separation: 0.035,
  damping: 0.97,
  nodeAlpha: 0.25,
  edgeAlpha: 0.05,
  fadeInFrames: 120,
  messageRate: 0.005,
  messageMinAge: 200,
  pulseSpeed: 2.4,
  pulseTrailAlpha: 0.1,
  pulseHeadRadius: 4,
  rippleDuration: 50,
  rippleMaxRadius: 25,
}

// Observe page: clusters spread across, avoiding center content
const clusters = [
  { cx: 0.08, cy: 0.25, spread: 0.1 },
  { cx: 0.92, cy: 0.3, spread: 0.1 },
  { cx: 0.1, cy: 0.7, spread: 0.12 },
  { cx: 0.9, cy: 0.75, spread: 0.1 },
  { cx: 0.5, cy: 0.9, spread: 0.15 },
]

export default function BackgroundCanvas() {
  const canvasRef = useRef(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const ctx = canvas.getContext('2d')
    let W, H, dpr
    const mouse = { x: -1000, y: -1000 }
    const nodes = []
    const pulses = []
    let tick = 0
    let lastFrame = 0
    let rafId

    function resize() {
      dpr = window.devicePixelRatio || 1
      W = window.innerWidth
      H = window.innerHeight
      canvas.width = W * dpr
      canvas.height = H * dpr
      canvas.style.width = W + 'px'
      canvas.style.height = H + 'px'
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    }

    function spawnNode() {
      const c = clusters[Math.floor(Math.random() * clusters.length)]
      const angle = Math.random() * Math.PI * 2
      const radius = (Math.random() + Math.random()) * 0.5 * c.spread
      nodes.push({
        x: (c.cx + Math.cos(angle) * radius) * W,
        y: (c.cy + Math.sin(angle) * radius) * H,
        cluster: c,
        vx: (Math.random() - 0.5) * 0.5,
        vy: (Math.random() - 0.5) * 0.5,
        age: 0,
        glow: 0,
      })
    }

    function sendPulse(from, to) {
      pulses.push({ fromNode: from, toNode: to, x: from.x, y: from.y, progress: 0, alive: true, age: 0 })
      from.glow = 1
    }

    function update() {
      tick++
      if (nodes.length < CONFIG.maxNodes && Math.random() < CONFIG.spawnRate) spawnNode()

      for (let i = 0; i < nodes.length; i++) {
        const n = nodes[i]
        n.age++
        n.glow *= 0.93
        n.vx += (Math.random() - 0.5) * CONFIG.drift
        n.vy += (Math.random() - 0.5) * CONFIG.drift

        let cx = 0, cy = 0, neighbors = 0
        const cohesionDistSq = CONFIG.connectionDistSq * 2.25
        for (let j = 0; j < nodes.length; j++) {
          if (i === j) continue
          const dx = nodes[j].x - n.x, dy = nodes[j].y - n.y
          const distSq = dx * dx + dy * dy
          if (distSq < cohesionDistSq) {
            cx += nodes[j].x; cy += nodes[j].y; neighbors++
            if (distSq < 2500 && distSq > 0) {
              const force = CONFIG.separation / Math.sqrt(distSq)
              n.vx -= dx * force; n.vy -= dy * force
            }
          }
        }
        if (neighbors > 0) {
          cx /= neighbors; cy /= neighbors
          n.vx += (cx - n.x) * CONFIG.cohesion
          n.vy += (cy - n.y) * CONFIG.cohesion
        }

        if (n.cluster) {
          n.vx += (n.cluster.cx * W - n.x) * 0.00008
          n.vy += (n.cluster.cy * H - n.y) * 0.00008
        }

        const mdx = n.x - mouse.x, mdy = n.y - mouse.y
        const mdistSq = mdx * mdx + mdy * mdy
        if (mdistSq < CONFIG.mouseRadiusSq && mdistSq > 0) {
          const mdist = Math.sqrt(mdistSq)
          const mf = (1 - mdist / CONFIG.mouseRadius) * CONFIG.mouseForce
          n.vx += (mdx / mdist) * mf * 8
          n.vy += (mdy / mdist) * mf * 8
        }

        n.vx *= CONFIG.damping; n.vy *= CONFIG.damping
        n.x += n.vx; n.y += n.vy

        const margin = 50
        if (n.x < -margin) n.x = W + margin
        if (n.x > W + margin) n.x = -margin
        if (n.y < -margin) n.y = H + margin
        if (n.y > H + margin) n.y = -margin

        if (n.age > CONFIG.messageMinAge && Math.random() < CONFIG.messageRate) {
          const candidates = []
          for (let k = 0; k < nodes.length; k++) {
            if (i === k) continue
            const b = nodes[k]
            if (b.age < CONFIG.fadeInFrames) continue
            const bx = b.x - n.x, by = b.y - n.y
            if (bx * bx + by * by < CONFIG.connectionDistSq) candidates.push(b)
          }
          if (candidates.length > 0) sendPulse(n, candidates[Math.floor(Math.random() * candidates.length)])
        }
      }

      for (let pi = 0; pi < pulses.length; pi++) {
        const p = pulses[pi]
        if (!p.alive) { p.age++; continue }
        p.age++
        const pdx = p.toNode.x - p.fromNode.x, pdy = p.toNode.y - p.fromNode.y
        const pdist = Math.sqrt(pdx * pdx + pdy * pdy)
        p.progress += CONFIG.pulseSpeed / Math.max(pdist, 1)
        let px = p.fromNode.x + pdx * p.progress
        let py = p.fromNode.y + pdy * p.progress
        const pmx = mouse.x - px, pmy = mouse.y - py
        const pmDist = Math.sqrt(pmx * pmx + pmy * pmy)
        if (pmDist < CONFIG.mouseRadius && pmDist > 0) {
          const bend = (1 - pmDist / CONFIG.mouseRadius) * 12
          px += (pmx / pmDist) * bend; py += (pmy / pmDist) * bend
        }
        p.x = px; p.y = py
        if (p.progress >= 1) { p.toNode.glow = 1; p.alive = false; p.age = 0 }
      }

      for (let ri = pulses.length - 1; ri >= 0; ri--) {
        if (!pulses[ri].alive && pulses[ri].age > CONFIG.rippleDuration) pulses.splice(ri, 1)
      }
    }

    function draw() {
      ctx.clearRect(0, 0, W, H)

      ctx.lineWidth = 0.8
      for (let i = 0; i < nodes.length; i++) {
        const a = nodes[i]
        const aAlpha = Math.min(1, a.age / CONFIG.fadeInFrames)
        if (aAlpha < 0.05) continue
        for (let j = i + 1; j < nodes.length; j++) {
          const b = nodes[j]
          const bAlpha = Math.min(1, b.age / CONFIG.fadeInFrames)
          if (bAlpha < 0.05) continue
          const dx = a.x - b.x, dy = a.y - b.y
          const distSq = dx * dx + dy * dy
          if (distSq < CONFIG.connectionDistSq) {
            const dist = Math.sqrt(distSq)
            const strength = 1 - dist / CONFIG.connectionDist
            const alpha = CONFIG.edgeAlpha * strength * aAlpha * bAlpha
            ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y)
            ctx.strokeStyle = 'rgba(210, 190, 160, ' + alpha + ')'
            ctx.stroke()
          }
        }
      }

      for (let pi = 0; pi < pulses.length; pi++) {
        const p = pulses[pi]
        if (p.alive) {
          const grad = ctx.createLinearGradient(p.fromNode.x, p.fromNode.y, p.x, p.y)
          grad.addColorStop(0, 'rgba(210, 190, 160, 0)')
          grad.addColorStop(0.6, 'rgba(210, 190, 160, ' + (CONFIG.pulseTrailAlpha * 0.3) + ')')
          grad.addColorStop(1, 'rgba(210, 190, 160, ' + CONFIG.pulseTrailAlpha + ')')
          ctx.beginPath(); ctx.moveTo(p.fromNode.x, p.fromNode.y); ctx.lineTo(p.x, p.y)
          ctx.strokeStyle = grad; ctx.lineWidth = 1.2; ctx.stroke()
          ctx.beginPath(); ctx.arc(p.x, p.y, CONFIG.pulseHeadRadius, 0, Math.PI * 2)
          ctx.fillStyle = 'rgba(210, 190, 160, ' + (CONFIG.pulseTrailAlpha * 0.7) + ')'; ctx.fill()
        } else {
          const t = p.age / CONFIG.rippleDuration
          if (t <= 1) {
            ctx.beginPath(); ctx.arc(p.toNode.x, p.toNode.y, t * CONFIG.rippleMaxRadius, 0, Math.PI * 2)
            ctx.strokeStyle = 'rgba(210, 190, 160, ' + ((1 - t) * 0.25) + ')'
            ctx.lineWidth = 0.9; ctx.stroke()
          }
        }
      }

      for (let ni = 0; ni < nodes.length; ni++) {
        const n = nodes[ni]
        const na = Math.min(1, n.age / CONFIG.fadeInFrames)
        if (na < 0.01) continue
        const baseAlpha = CONFIG.nodeAlpha * na
        const glowAdd = n.glow * 0.4
        if (n.glow > 0.05) {
          ctx.beginPath(); ctx.arc(n.x, n.y, CONFIG.nodeRadius + 3 + n.glow * 5, 0, Math.PI * 2)
          ctx.strokeStyle = 'rgba(210, 190, 160, ' + (n.glow * 0.15) + ')'
          ctx.lineWidth = 0.8; ctx.stroke()
        }
        ctx.beginPath(); ctx.arc(n.x, n.y, CONFIG.nodeRadius, 0, Math.PI * 2)
        ctx.fillStyle = 'rgba(210, 190, 160, ' + (baseAlpha + glowAdd) + ')'; ctx.fill()
      }
    }

    function loop(timestamp) {
      rafId = requestAnimationFrame(loop)
      if (timestamp - lastFrame < 32) return
      lastFrame = timestamp
      update(); draw()
    }

    const onMouseMove = (e) => { mouse.x = e.clientX; mouse.y = e.clientY }
    const onMouseLeave = () => { mouse.x = -1000; mouse.y = -1000 }
    const onTouchStart = (e) => { const t = e.touches[0]; mouse.x = t.clientX; mouse.y = t.clientY }
    const onTouchMove = (e) => { const t = e.touches[0]; mouse.x = t.clientX; mouse.y = t.clientY }
    const onTouchEnd = () => { mouse.x = -1000; mouse.y = -1000 }

    window.addEventListener('resize', resize)
    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseleave', onMouseLeave)
    window.addEventListener('touchstart', onTouchStart, { passive: true })
    window.addEventListener('touchmove', onTouchMove, { passive: true })
    window.addEventListener('touchend', onTouchEnd, { passive: true })

    resize()
    rafId = requestAnimationFrame(loop)

    return () => {
      cancelAnimationFrame(rafId)
      window.removeEventListener('resize', resize)
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseleave', onMouseLeave)
      window.removeEventListener('touchstart', onTouchStart)
      window.removeEventListener('touchmove', onTouchMove)
      window.removeEventListener('touchend', onTouchEnd)
    }
  }, [])

  return <canvas id="bg" ref={canvasRef} />
}
