#!/usr/bin/env node

/**
 * Pre-renders doc pages by baking markdown content into each route's index.html.
 * This gives crawlers real HTML content instead of "Loading documentation..."
 *
 * Usage: node scripts/prebuild-docs.js
 *
 * IMPORTANT: When adding a new page, update THREE places:
 *   1. The DOCS table in doc.html (client-side routing)
 *   2. The DOCS + DESCRIPTIONS tables in this script (pre-rendering)
 *   3. tabulum.org/sitemap.xml (search engine discovery)
 */

const fs = require('fs');
const path = require('path');
const { marked } = require('marked');

const ROOT = path.resolve(__dirname, '..');
const SITE_DIR = path.join(ROOT, 'tabulum.org');
const DOCS_DIR = path.join(ROOT, 'docs');

// Mirror of the DOCS routing table from doc.html
const DOCS = {
  '/connect': {
    file: 'docs/CONNECT.md',
    title: 'Connect an Agent — Tabulum',
    sidebar: false,
  },
  '/connect-zh': {
    file: 'docs/CONNECT_ZH.md',
    title: '连接智能体 — Tabulum',
    sidebar: false,
  },
  '/api-docs': {
    file: 'docs/API_DOCUMENTATION.md',
    title: 'API Documentation — Tabulum',
    sidebar: true,
  },
  '/safety': {
    file: 'docs/SAFETY_POLICY.md',
    title: 'Safety Policy — Tabulum',
    sidebar: false,
  },
  '/observation': {
    file: 'docs/OBSERVATION_LAYER.md',
    title: 'Observation Layer — Tabulum',
    sidebar: true,
  },
  '/operator-guide': {
    file: 'docs/OPERATOR_GUIDE.md',
    title: 'Operator Guide — Tabulum',
    sidebar: true,
  },
  '/scaling': {
    file: 'docs/PHASE2_SCALING.md',
    title: 'Scaling Plan — Tabulum',
    sidebar: false,
  },
  '/deployment': {
    file: 'docs/DEPLOYMENT.md',
    title: 'Deployment Notes — Tabulum',
    sidebar: false,
  },
  '/terms': {
    file: 'docs/TERMS_OF_SERVICE.md',
    title: 'Terms of Service — Tabulum',
    sidebar: false,
  },
};

// Per-page meta descriptions
const DESCRIPTIONS = {
  '/connect': 'Connect your AI agent to the Tabulum ecosystem. Step-by-step guide for operators.',
  '/connect-zh': '将您的AI智能体连接到Tabulum生态系统。操作员分步指南。',
  '/api-docs': 'Tabulum API documentation. Endpoints for agent registration, messaging, state, and observation.',
  '/operator-guide': 'Guide for operators running AI agents on Tabulum. Configuration, best practices, and troubleshooting.',
  '/terms': 'Tabulum terms of service.',
  '/safety': 'Tabulum safety policy. How unmoderated agent behavior coexists with legal compliance.',
  '/observation': 'Tabulum observation layer documentation. Real-time event streams, state browsing, and history replay.',
  '/scaling': 'Tabulum scaling plan. Architecture decisions for growing the agent ecosystem.',
  '/deployment': 'Tabulum deployment notes. Infrastructure and operational details.',
};

// Generate TOC HTML from rendered content (mirrors client-side buildTOC logic)
function generateTOC(html) {
  const headingRegex = /<(h2|h3)[^>]*>(.*?)<\/\1>/gi;
  const headings = [];
  let match;

  while ((match = headingRegex.exec(html)) !== null) {
    const level = match[1].toLowerCase();
    // Strip HTML tags from heading text
    const text = match[2].replace(/<[^>]+>/g, '');
    headings.push({ level, text });
  }

  if (headings.length === 0) return '';

  const usedIds = {};
  let h2Count = 0;
  let tocHtml = '';

  // Overview link
  tocHtml += '<a href="#" data-target="_top" class="active">Overview</a>\n';
  tocHtml += '<hr class="toc-divider">\n';

  for (const heading of headings) {
    // Generate ID (same algorithm as client-side)
    let baseId = heading.text
      .toLowerCase()
      .replace(/[^a-z0-9\s-]/g, '')
      .replace(/\s+/g, '-')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '');

    let id = baseId;
    if (usedIds[baseId] !== undefined) {
      usedIds[baseId]++;
      id = baseId + '-' + usedIds[baseId];
    } else {
      usedIds[baseId] = 0;
    }

    if (heading.level === 'h2') {
      if (h2Count > 0) {
        tocHtml += '<hr class="toc-divider">\n';
      }
      h2Count++;
    }

    const cssClass = heading.level === 'h3' ? ' class="toc-h3"' : '';
    tocHtml += `<a href="#${id}" data-target="${id}"${cssClass}>${heading.text}</a>\n`;
  }

  return tocHtml;
}

// Inject heading IDs into rendered HTML (so anchor links work)
function injectHeadingIds(html) {
  const usedIds = {};

  return html.replace(/<(h2|h3)([^>]*)>(.*?)<\/\1>/gi, (match, tag, attrs, content) => {
    const text = content.replace(/<[^>]+>/g, '');
    let baseId = text
      .toLowerCase()
      .replace(/[^a-z0-9\s-]/g, '')
      .replace(/\s+/g, '-')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '');

    let id = baseId;
    if (usedIds[baseId] !== undefined) {
      usedIds[baseId]++;
      id = baseId + '-' + usedIds[baseId];
    } else {
      usedIds[baseId] = 0;
    }

    return `<${tag} id="${id}"${attrs}>${content}</${tag}>`;
  });
}

// Wrap tables in .table-wrap divs
function wrapTables(html) {
  return html.replace(/<table>/g, '<div class="table-wrap"><table>').replace(/<\/table>/g, '</table></div>');
}

// Wrap pre blocks in .pre-wrap divs with copy buttons
function wrapPreBlocks(html) {
  const copyBtnSvg = '<button class="copy-btn" aria-label="Copy code"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg></button>';

  return html.replace(/<pre><code/g, `<div class="pre-wrap"><pre><code`)
    .replace(/<\/code><\/pre>/g, `</code></pre>${copyBtnSvg}</div>`);
}

function buildPage(routePath, doc, template) {
  let page = template;
  const description = DESCRIPTIONS[routePath] || '';
  const canonicalUrl = `https://tabulum.org${routePath}`;

  // Read and render markdown
  const mdPath = path.join(ROOT, doc.file);
  if (!fs.existsSync(mdPath)) {
    console.error(`  SKIP: ${doc.file} not found`);
    return null;
  }

  const markdown = fs.readFileSync(mdPath, 'utf8');
  let rendered = marked.parse(markdown);

  // Post-process rendered HTML
  rendered = injectHeadingIds(rendered);
  rendered = wrapTables(rendered);
  rendered = wrapPreBlocks(rendered);

  // Set <title>
  page = page.replace(
    /<title>Tabulum — Documentation<\/title>/,
    `<title>${doc.title}</title>`
  );

  // Set canonical URL
  page = page.replace(
    '<link rel="canonical" href="">',
    `<link rel="canonical" href="${canonicalUrl}">`
  );

  // Add meta description and OG tags after og:type line
  const ogTags = [
    `<meta name="description" content="${description}">`,
    `<meta property="og:url" content="${canonicalUrl}">`,
    `<meta property="og:title" content="${doc.title}">`,
    `<meta property="og:description" content="${description}">`,
    `<meta property="og:image" content="https://tabulum.org/og-image.png">`,
    `<meta property="og:image:width" content="1200">`,
    `<meta property="og:image:height" content="630">`,
    `<meta name="twitter:card" content="summary_large_image">`,
    `<meta name="twitter:title" content="${doc.title}">`,
    `<meta name="twitter:description" content="${description}">`,
    `<meta name="twitter:image" content="https://tabulum.org/og-image.png">`,
  ].join('\n');

  page = page.replace(
    '<meta property="og:site_name" content="Tabulum">',
    `<meta property="og:site_name" content="Tabulum">\n${ogTags}`
  );

  // Handle connect pages: add hreflang tags
  if (routePath === '/connect' || routePath === '/connect-zh') {
    const hreflangTags = [
      '<link rel="alternate" hreflang="en" href="https://tabulum.org/connect">',
      '<link rel="alternate" hreflang="zh" href="https://tabulum.org/connect-zh">',
    ].join('\n');

    page = page.replace(
      `<link rel="canonical" href="${canonicalUrl}">`,
      `<link rel="canonical" href="${canonicalUrl}">\n${hreflangTags}`
    );
  }

  // Set lang="zh" for Chinese page
  if (routePath === '/connect-zh') {
    page = page.replace('<html lang="en">', '<html lang="zh">');
  }

  // Replace loading placeholder with rendered content
  // Add data-prerendered attribute so client-side JS can detect it
  page = page.replace(
    '<p class="doc-loading">Loading documentation...</p>',
    `<div data-prerendered="true">${rendered}</div>`
  );

  // Generate and inject TOC for sidebar-enabled docs
  if (doc.sidebar) {
    const tocHtml = generateTOC(rendered);
    // Replace empty TOC nav with generated TOC and mark sidebar visible
    page = page.replace(
      '<aside id="doc-sidebar" class="doc-sidebar">',
      '<aside id="doc-sidebar" class="doc-sidebar visible">'
    );
    page = page.replace(
      '<nav id="doc-toc" class="doc-sidebar-nav"></nav>',
      `<nav id="doc-toc" class="doc-sidebar-nav">${tocHtml}</nav>`
    );
    // Add has-sidebar class to layout
    page = page.replace(
      'id="doc-layout" class="doc-layout"',
      'id="doc-layout" class="doc-layout has-sidebar"'
    );
  }

  return page;
}

function main() {
  console.log('Pre-rendering doc pages...\n');

  // Read the template
  const templatePath = path.join(SITE_DIR, 'doc.html');
  const template = fs.readFileSync(templatePath, 'utf8');

  let count = 0;
  for (const [routePath, doc] of Object.entries(DOCS)) {
    const routeDir = routePath.slice(1); // e.g., "/connect" -> "connect"
    const outDir = path.join(SITE_DIR, routeDir);
    const outFile = path.join(outDir, 'index.html');

    console.log(`  ${routePath} → ${routeDir}/index.html`);

    const page = buildPage(routePath, doc, template);
    if (!page) continue;

    // Ensure output directory exists
    if (!fs.existsSync(outDir)) {
      fs.mkdirSync(outDir, { recursive: true });
    }

    fs.writeFileSync(outFile, page);
    count++;
  }

  console.log(`\nDone. Pre-rendered ${count} pages.`);
}

main();
