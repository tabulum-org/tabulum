#!/usr/bin/env node
/**
 * Generate OG image for tabulum.org — snapshot of the landing page animation.
 * Uses node-canvas (native anti-aliased rendering, same Canvas 2D API as browser).
 * Runs the actual physics simulation from index.html to get clustered nodes.
 *
 * Prerequisites (fonts must be downloaded before running):
 *   mkdir -p /tmp/tabulum-fonts && cd /tmp/tabulum-fonts
 *   # DM Serif Display — get URL from: fonts.googleapis.com/css2?family=DM+Serif+Display
 *   curl -sL "https://fonts.gstatic.com/s/dmserifdisplay/v17/..." -o DMSerifDisplay-Regular.ttf
 *   # JetBrains Mono — from github.com/JetBrains/JetBrainsMono/releases
 *   curl -sL "https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip" -o jb.zip && unzip jb.zip -d jb-mono
 *   # Caveat — get URL from: fonts.googleapis.com/css2?family=Caveat:wght@700
 *   curl -sL "https://fonts.gstatic.com/s/caveat/v23/..." -o Caveat-Bold.ttf
 *
 * Then: npm install canvas && node scripts/gen-og-image.js
 */

const { createCanvas, registerFont } = require('canvas');
const fs = require('fs');
const path = require('path');

// Register fonts
registerFont('/tmp/tabulum-fonts/DMSerifDisplay-Regular.ttf', { family: 'DM Serif Display' });
registerFont('/tmp/tabulum-fonts/jb-mono/fonts/ttf/JetBrainsMono-SemiBold.ttf', { family: 'JetBrains Mono', weight: '600' });
registerFont('/tmp/tabulum-fonts/jb-mono/fonts/ttf/JetBrainsMono-Medium.ttf', { family: 'JetBrains Mono', weight: '500' });
registerFont('/tmp/tabulum-fonts/Caveat-Bold.ttf', { family: 'Caveat', weight: '700' });

const W = 1200, H = 630;

// Use 2x DPI for retina-quality output
const DPR = 2;
const canvas = createCanvas(W * DPR, H * DPR);
const ctx = canvas.getContext('2d');
ctx.scale(DPR, DPR);

// Enable anti-aliasing (default, but explicit)
ctx.imageSmoothingEnabled = true;
ctx.imageSmoothingQuality = 'high';

// Colors
const BG = '#0a0a0c';
const TEXT = '#e8e6e1';
const GOLD = 'rgba(210, 190, 160, ';
const ACCENT = '#92B890';

// --- CONFIG from index.html ---
const CONFIG = {
  maxNodes: 36,
  nodeRadius: 5,
  connectionDist: 320,
  connectionDistSq: 320 * 320,
  nodeAlpha: 0.3,
  edgeAlpha: 0.06,
  drift: 0.12,
  cohesion: 0.00015,
  separation: 0.035,
  damping: 0.97,
};

const clusters = [
  { cx: 0.55, cy: 0.55, spread: 0.15 },
  { cx: 0.78, cy: 0.3,  spread: 0.18 },
  { cx: 0.92, cy: 0.65, spread: 0.15 },
];

// --- Seed random for reproducibility ---
let seed = 42;
function random() {
  seed = (seed * 16807 + 0) % 2147483647;
  return (seed - 1) / 2147483646;
}

// --- Spawn nodes (same algo as index.html) ---
const nodes = [];
for (let i = 0; i < CONFIG.maxNodes; i++) {
  const c = clusters[Math.floor(random() * clusters.length)];
  const angle = random() * Math.PI * 2;
  const radius = (random() + random()) * 0.5 * c.spread;
  nodes.push({
    x: (c.cx + Math.cos(angle) * radius) * W,
    y: (c.cy + Math.sin(angle) * radius) * H,
    cluster: c,
    vx: (random() - 0.5) * 0.5,
    vy: (random() - 0.5) * 0.5,
    glow: 0,
  });
}

// --- Run physics simulation (same as index.html update()) ---
const cohesionDistSq = CONFIG.connectionDistSq * 2.25;

for (let tick = 0; tick < 500; tick++) {
  for (let i = 0; i < nodes.length; i++) {
    const n = nodes[i];
    n.glow *= 0.93;
    n.vx += (random() - 0.5) * CONFIG.drift;
    n.vy += (random() - 0.5) * CONFIG.drift;

    let cx = 0, cy = 0, neighbors = 0;
    for (let j = 0; j < nodes.length; j++) {
      if (i === j) continue;
      const dx = nodes[j].x - n.x;
      const dy = nodes[j].y - n.y;
      const distSq = dx * dx + dy * dy;
      if (distSq < cohesionDistSq) {
        cx += nodes[j].x;
        cy += nodes[j].y;
        neighbors++;
        if (distSq < 2500 && distSq > 0) {
          const force = CONFIG.separation / Math.sqrt(distSq);
          n.vx -= dx * force;
          n.vy -= dy * force;
        }
      }
    }
    if (neighbors > 0) {
      cx /= neighbors; cy /= neighbors;
      n.vx += (cx - n.x) * CONFIG.cohesion;
      n.vy += (cy - n.y) * CONFIG.cohesion;
    }

    n.vx += (n.cluster.cx * W - n.x) * 0.00008;
    n.vy += (n.cluster.cy * H - n.y) * 0.00008;

    n.vx *= CONFIG.damping;
    n.vy *= CONFIG.damping;
    n.x += n.vx;
    n.y += n.vy;

    const margin = 50;
    if (n.x < -margin) n.x = W + margin;
    if (n.x > W + margin) n.x = -margin;
    if (n.y < -margin) n.y = H + margin;
    if (n.y > H + margin) n.y = -margin;
  }
}

// --- Build connections ---
const connections = [];
for (let i = 0; i < nodes.length; i++) {
  for (let j = i + 1; j < nodes.length; j++) {
    const dx = nodes[i].x - nodes[j].x;
    const dy = nodes[i].y - nodes[j].y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist < CONFIG.connectionDist) {
      connections.push({ i, j, dist });
    }
  }
}

// --- Set up pulse activity ---
// 3-4 pulses in flight
const pulses = [];
const usedSrc = new Set();
const pulseCandidates = [[2, 0.55], [18, 0.35], [28, 0.7], [9, 0.5]];
for (const [srcIdx, progress] of pulseCandidates) {
  if (srcIdx >= nodes.length || usedSrc.has(srcIdx)) continue;
  const src = nodes[srcIdx];
  for (const conn of connections) {
    const dstIdx = conn.i === srcIdx ? conn.j : (conn.j === srcIdx ? conn.i : null);
    if (dstIdx !== null && !usedSrc.has(dstIdx)) {
      pulses.push({ src, dst: nodes[dstIdx], progress });
      src.glow = 0.85;
      usedSrc.add(srcIdx);
      usedSrc.add(dstIdx);
      break;
    }
  }
}

// Subtle glow on a few nodes
for (const idx of [4, 8, 15, 24, 33]) {
  if (idx < nodes.length) {
    nodes[idx].glow = 0.3 + random() * 0.3;
  }
}

// Pick ~15% of edges to highlight
const highlightEdges = new Set();
seed = 99;
for (let k = 0; k < Math.floor(connections.length / 6); k++) {
  highlightEdges.add(Math.floor(random() * connections.length));
}
seed = 42;

// --- DRAW ---

// Background
ctx.fillStyle = BG;
ctx.fillRect(0, 0, W, H);

// Draw edges (same as index.html draw())
ctx.lineWidth = 0.8;
for (let ci = 0; ci < connections.length; ci++) {
  const { i, j, dist } = connections[ci];
  const a = nodes[i], b = nodes[j];
  const strength = 1 - dist / CONFIG.connectionDist;

  if (highlightEdges.has(ci)) {
    ctx.strokeStyle = GOLD + (0.16 * strength) + ')';
    ctx.lineWidth = 1.2;
  } else {
    ctx.strokeStyle = GOLD + (CONFIG.edgeAlpha * strength) + ')';
    ctx.lineWidth = 0.8;
  }

  ctx.beginPath();
  ctx.moveTo(a.x, a.y);
  ctx.lineTo(b.x, b.y);
  ctx.stroke();
}

// Draw pulse trails (same as index.html)
for (const p of pulses) {
  const sx = p.src.x, sy = p.src.y;
  const dx = p.dst.x, dy = p.dst.y;
  const px = sx + (dx - sx) * p.progress;
  const py = sy + (dy - sy) * p.progress;

  // Trail gradient
  const grad = ctx.createLinearGradient(sx, sy, px, py);
  grad.addColorStop(0, GOLD + '0)');
  grad.addColorStop(0.6, GOLD + (0.12 * 0.3) + ')');
  grad.addColorStop(1, GOLD + '0.12)');
  ctx.beginPath();
  ctx.moveTo(sx, sy);
  ctx.lineTo(px, py);
  ctx.strokeStyle = grad;
  ctx.lineWidth = 1.2;
  ctx.stroke();

  // Pulse head
  ctx.beginPath();
  ctx.arc(px, py, CONFIG.nodeRadius, 0, Math.PI * 2);
  ctx.fillStyle = GOLD + (0.12 * 0.7) + ')';
  ctx.fill();
}

// Draw ripples at a couple destination nodes
for (const ridx of [15, 24]) {
  if (ridx >= nodes.length) continue;
  const rn = nodes[ridx];
  for (const ringR of [10, 18, 25]) {
    const t = ringR / 30;
    ctx.beginPath();
    ctx.arc(rn.x, rn.y, ringR, 0, Math.PI * 2);
    ctx.strokeStyle = GOLD + ((1 - t) * 0.25) + ')';
    ctx.lineWidth = 0.9;
    ctx.stroke();
  }
}

// Draw nodes (same as index.html draw())
for (const n of nodes) {
  const baseAlpha = CONFIG.nodeAlpha;
  const glowAdd = n.glow * 0.4;

  // Glow ring for highlighted nodes
  if (n.glow > 0.05) {
    ctx.beginPath();
    ctx.arc(n.x, n.y, CONFIG.nodeRadius + 3 + n.glow * 5, 0, Math.PI * 2);
    ctx.strokeStyle = GOLD + (n.glow * 0.15) + ')';
    ctx.lineWidth = 0.8;
    ctx.stroke();
  }

  // Node fill
  ctx.beginPath();
  ctx.arc(n.x, n.y, CONFIG.nodeRadius, 0, Math.PI * 2);
  ctx.fillStyle = GOLD + (baseAlpha + glowAdd) + ')';
  ctx.fill();
}

// --- Typography ---
// TABULUM
ctx.font = '600 36px "JetBrains Mono"';
ctx.letterSpacing = '0.14em';
ctx.fillStyle = TEXT;
ctx.fillText('TABULUM', 65, 75);

// Subtitle — midway between TABULUM and hero
ctx.font = '500 22px "JetBrains Mono"';
ctx.letterSpacing = '0';
ctx.fillStyle = 'rgb(210, 190, 160)';
ctx.fillText('An ecosystem for autonomous agents', 65, 145);

// Hero text
const heroTop = 270;
const lineH = 76;
ctx.font = '62px "DM Serif Display"';
ctx.letterSpacing = '0';
ctx.fillStyle = ACCENT;

ctx.fillText('The blank surface', 65, heroTop);

// "on which" then "anything" in Caveat
ctx.fillText('on which ', 65, heroTop + lineH);
const onWhichWidth = ctx.measureText('on which ').width;

// Baseline-align Caveat "anything"
ctx.font = '700 72px "Caveat"';
ctx.fillStyle = TEXT;
ctx.fillText('anything', 65 + onWhichWidth, heroTop + lineH);

// "could be written"
ctx.font = '62px "DM Serif Display"';
ctx.fillStyle = ACCENT;
ctx.fillText('could be written', 65, heroTop + lineH * 2);

// --- Output ---
const outPath = path.join(__dirname, '..', 'tabulum.org', 'og-image.png');
const buf = canvas.toBuffer('image/png');
fs.writeFileSync(outPath, buf);
console.log(`Generated og-image.png (${W}x${H}, ${DPR}x DPI, ${buf.length} bytes)`);
