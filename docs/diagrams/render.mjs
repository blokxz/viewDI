// Renders the documentation diagrams: src/<name>.json → docs/img/<name>.png.
//
// ELK (elkjs, "layered" algorithm) places the boxes and routes every edge
// orthogonally (straight segments, 90-degree turns); the SVG is drawn here
// with rounded corners and turned into a PNG by headless Chromium, which
// also measures the text with real fonts.
//
// Usage (from the repository root):
//   make docs-diagrams                      # every diagram
//   node docs/diagrams/render.mjs supervisor  # only some
// VIEWDI_DIAGRAM_OUT writes elsewhere; CHROMIUM_PATH picks a browser binary.
//
// Spec format (src/*.json):
//   title, direction ("DOWN" | "RIGHT"), spacing (optional ELK overrides),
//   groups: [{id, title, parent?}],
//   nodes:  [{id, title, detail?, role?, icon?, group?}]   (order = layout hint)
//   edges:  [{from, to (id or list), label?, color?, dashed?, both?, bus?}]
//   Edges with the same bus name end at one port of their target, so they
//   merge into a single trunk with one arrowhead.
// Roles: ui, state, provider, adapter, external, good, warn, bad, neutral.
// Colours: ink (default), data, control, error, ok, muted.

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const require = createRequire(import.meta.url);
const here = path.dirname(fileURLToPath(import.meta.url));
const outDir = process.env.VIEWDI_DIAGRAM_OUT || path.join(here, '..', 'img');

const icons = Object.fromEntries(
  fs.readdirSync(path.join(here, 'icons')).map((f) => [
    path.basename(f, '.png'),
    'data:image/png;base64,' + fs.readFileSync(path.join(here, 'icons', f)).toString('base64'),
  ]),
);

const names = process.argv.slice(2).length
  ? process.argv.slice(2)
  : fs.readdirSync(path.join(here, 'src')).filter((f) => f.endsWith('.json')).map((f) => path.basename(f, '.json')).sort();

const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined });
try {
  const page = await browser.newPage({ deviceScaleFactor: 2 });
  await page.setContent('<!doctype html><html><body style="margin:0;background:#fff"></body></html>');
  await page.addScriptTag({ path: require.resolve('elkjs/lib/elk.bundled.js') });
  fs.mkdirSync(outDir, { recursive: true });
  for (const name of names) {
    const spec = JSON.parse(fs.readFileSync(path.join(here, 'src', `${name}.json`), 'utf8'));
    const size = await page.evaluate(draw, { spec, icons });
    await page.setViewportSize({ width: size.width, height: size.height });
    const file = path.join(outDir, `${name}.png`);
    await page.screenshot({ path: file, clip: { x: 0, y: 0, width: size.width, height: size.height } });
    console.log(`${name}: ${size.width}x${size.height} → ${file}`);
  }
} finally {
  await browser.close();
}

// draw runs inside the page: layout with ELK, then an SVG in the body.
async function draw({ spec, icons }) {
  const ROLES = {
    ui: ['#e4f1ec', '#5f9f8a'],
    state: ['#eee8f6', '#8c6bb1'],
    provider: ['#fdf3e1', '#c9933e'],
    adapter: ['#e6effa', '#4f86c6'],
    external: ['#f3f4f4', '#8a9196'],
    good: ['#ecf4e0', '#7f9f4a'],
    warn: ['#fdf3d7', '#c9a227'],
    bad: ['#fbe6e6', '#c96b6b'],
    neutral: ['#ffffff', '#8a9196'],
  };
  const COLORS = { ink: '#3c474d', data: '#5f9f8a', control: '#d0784e', error: '#c96b6b', ok: '#7f9f4a', muted: '#9aa1a6' };
  const INK = '#3c474d';
  const DETAIL = '#69757b';
  const FONT = 'Helvetica, Arial, sans-serif';
  const RADIUS = 9;
  const TITLE_H = 44;

  const ctx = document.createElement('canvas').getContext('2d');
  const measure = (text, px, bold) => {
    ctx.font = `${bold ? 'bold ' : ''}${px}px ${FONT}`;
    return ctx.measureText(text).width;
  };
  const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const lines = (s) => (s || '').split('\n').filter(Boolean);

  // ----- ELK graph -----
  const elkNodes = {};
  const cardNode = (n) => {
    const detail = lines(n.detail);
    const textW = Math.max(measure(n.title, 13, true), ...detail.map((l) => measure(l, 11)));
    const iconW = n.icon ? 34 : 0;
    const node = { id: n.id, width: Math.ceil(textW + iconW + 30), height: 34 + detail.length * 14 };
    elkNodes[n.id] = node;
    return node;
  };
  const groupNode = (g) => {
    const node = {
      id: g.id,
      layoutOptions: { 'elk.padding': '[top=36,left=14,bottom=14,right=14]' },
      children: [],
    };
    elkNodes[g.id] = node;
    return node;
  };
  const root = {
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': spec.direction || 'DOWN',
      'elk.edgeRouting': 'ORTHOGONAL',
      'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
      'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX',
      'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
      // Edges pointing back in the spec's node order close cycles (retry loops).
      'elk.layered.cycleBreaking.strategy': 'MODEL_ORDER',
      'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
      'elk.layered.thoroughness': '30',
      'elk.layered.unnecessaryBendpoints': 'true',
      'elk.layered.mergeEdges': 'false',
      'elk.edgeLabels.inline': 'true',
      'elk.layered.spacing.nodeNodeBetweenLayers': '46',
      'elk.layered.spacing.edgeNodeBetweenLayers': '18',
      'elk.layered.spacing.edgeEdgeBetweenLayers': '14',
      'elk.spacing.nodeNode': '26',
      'elk.spacing.edgeNode': '18',
      'elk.spacing.edgeEdge': '14',
      'elk.padding': '[top=16,left=20,bottom=20,right=20]',
      ...(spec.spacing || {}),
    },
    children: [],
    edges: [],
  };
  const groups = spec.groups || [];
  for (const g of groups) groupNode(g);
  const parentOf = (groupId) => (groupId ? elkNodes[groupId] : root);
  // Nodes keep their spec order, which ELK uses as a hint; a group takes the
  // place of its first member.
  const placed = new Set();
  const place = (groupId) => {
    if (!groupId || placed.has(groupId)) return;
    const grp = groups.find((x) => x.id === groupId);
    if (!grp) throw new Error(`unknown group ${groupId}`);
    place(grp.parent);
    parentOf(grp.parent).children.push(elkNodes[groupId]);
    placed.add(groupId);
  };
  for (const n of spec.nodes) {
    place(n.group);
    parentOf(n.group).children.push(cardNode(n));
  }

  const inSide = (spec.direction || 'DOWN') === 'RIGHT' ? 'WEST' : 'NORTH';
  const busPort = (nodeId, bus) => {
    const node = elkNodes[nodeId];
    const id = `${nodeId}__${bus}`;
    node.ports = node.ports || [];
    if (!node.ports.some((p) => p.id === id)) {
      node.ports.push({ id, width: 1, height: 1, layoutOptions: { 'elk.port.side': inSide } });
      node.layoutOptions = { ...(node.layoutOptions || {}), 'elk.portConstraints': 'FIXED_SIDE' };
    }
    return id;
  };
  let k = 0;
  for (const e of spec.edges) {
    const targets = Array.isArray(e.to) ? e.to : [e.to];
    targets.forEach((to, i) => {
      const target = e.bus ? busPort(to, e.bus) : to;
      const edge = { id: `e${k++}`, sources: [e.from], targets: [target], data: e };
      if (e.label && i === 0) {
        const ls = e.label.split('\n');
        edge.labels = [{ text: e.label, width: Math.ceil(Math.max(...ls.map((l) => measure(l, 10.5))) + 8), height: ls.length * 13 + 4 }];
      }
      root.edges.push(edge);
    });
  }

  const g = await new ELK().layout(root);

  // Absolute positions of every node and group.
  const abs = {};
  const walk = (n, ox, oy) => {
    abs[n.id] = { x: ox + (n.x || 0), y: oy + (n.y || 0), w: n.width, h: n.height };
    for (const c of n.children || []) walk(c, abs[n.id].x, abs[n.id].y);
  };
  walk(g, 0, 0);

  // ----- SVG -----
  const svg = [];
  const markers = new Set();
  const front = []; // drawn last: edge labels and group titles

  const depth = (grp) => (grp.parent ? 1 + depth(groups.find((x) => x.id === grp.parent)) : 0);
  for (const grp of [...groups].sort((a, b) => depth(a) - depth(b))) {
    const a = abs[grp.id];
    const fill = depth(grp) % 2 ? '#ffffff' : '#f7f9f8';
    svg.push(`<rect x="${a.x}" y="${a.y}" width="${a.w}" height="${a.h}" rx="12" fill="${fill}" stroke="#cfd6d2" stroke-width="1.2"/>`);
    // The title sits above the edges, on a backing of the group's colour.
    const tw = measure(grp.title, 12);
    front.push(`<rect x="${a.x + 9}" y="${a.y + 9}" width="${tw + 10}" height="18" rx="4" fill="${fill}"/>`);
    front.push(`<text x="${a.x + 14}" y="${a.y + 22}" font-size="12" fill="${DETAIL}">${esc(grp.title)}</text>`);
  }

  const rounded = (pts) => {
    let d = `M${pts[0].x},${pts[0].y}`;
    for (let i = 1; i < pts.length - 1; i++) {
      const p = pts[i - 1], c = pts[i], n = pts[i + 1];
      const d1 = Math.hypot(c.x - p.x, c.y - p.y), d2 = Math.hypot(n.x - c.x, n.y - c.y);
      if (!d1 || !d2) continue;
      const r = Math.min(RADIUS, d1 / 2, d2 / 2);
      const a = { x: c.x + ((p.x - c.x) * r) / d1, y: c.y + ((p.y - c.y) * r) / d1 };
      const b = { x: c.x + ((n.x - c.x) * r) / d2, y: c.y + ((n.y - c.y) * r) / d2 };
      d += ` L${a.x},${a.y} Q${c.x},${c.y} ${b.x},${b.y}`;
    }
    const last = pts[pts.length - 1];
    return `${d} L${last.x},${last.y}`;
  };

  for (const e of g.edges) {
    const off = abs[e.container || 'root'];
    const col = COLORS[e.data.color || 'ink'];
    const id = col.slice(1);
    markers.add(col);
    for (const s of e.sections || []) {
      const pts = [s.startPoint, ...(s.bendPoints || []), s.endPoint].map((p) => ({ x: p.x + off.x, y: p.y + off.y }));
      const dash = e.data.dashed ? ' stroke-dasharray="6 4"' : '';
      const start = e.data.both ? ` marker-start="url(#s${id})"` : '';
      svg.push(`<path d="${rounded(pts)}" fill="none" stroke="${col}" stroke-width="1.4"${dash} marker-end="url(#m${id})"${start}/>`);
    }
    for (const lb of e.labels || []) {
      const x = lb.x + off.x, y = lb.y + off.y;
      const fg = ['ink', 'muted'].includes(e.data.color || 'ink') ? DETAIL : col;
      front.push(`<rect x="${x - 2}" y="${y}" width="${lb.width + 4}" height="${lb.height}" rx="3" fill="#fff" fill-opacity="0.92"/>`);
      lb.text.split('\n').forEach((t, i) => {
        front.push(`<text x="${x + lb.width / 2}" y="${y + 12 + i * 13}" text-anchor="middle" font-size="10.5" fill="${fg}">${esc(t)}</text>`);
      });
    }
  }

  for (const n of spec.nodes) {
    const a = abs[n.id];
    const [fill, stroke] = ROLES[n.role || 'provider'];
    svg.push(`<rect x="${a.x}" y="${a.y}" width="${a.w}" height="${a.h}" rx="${RADIUS}" fill="${fill}" stroke="${stroke}" stroke-width="1.4"/>`);
    let tx = a.x + 15;
    if (n.icon) {
      if (!icons[n.icon]) throw new Error(`unknown icon ${n.icon}`);
      svg.push(`<image href="${icons[n.icon]}" x="${a.x + 12}" y="${a.y + a.h / 2 - 12}" width="24" height="24"/>`);
      tx += 34;
    }
    const detail = lines(n.detail);
    const top = a.y + a.h / 2 - (16 + detail.length * 14) / 2;
    svg.push(`<text x="${tx}" y="${top + 13}" font-size="13" font-weight="bold" fill="${INK}">${esc(n.title)}</text>`);
    detail.forEach((l, i) => svg.push(`<text x="${tx}" y="${top + 30 + i * 14}" font-size="11" fill="${DETAIL}">${esc(l)}</text>`));
  }

  const defs = [...markers]
    .map((c) => {
      const id = c.slice(1);
      return (
        `<marker id="m${id}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0,1 L10,5 L0,9 z" fill="${c}"/></marker>` +
        `<marker id="s${id}" viewBox="0 0 10 10" refX="1" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M10,1 L0,5 L10,9 z" fill="${c}"/></marker>`
      );
    })
    .join('');
  const width = Math.ceil(g.width);
  const height = Math.ceil(g.height + TITLE_H);
  document.body.innerHTML =
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" font-family="${FONT}">` +
    `<defs>${defs}</defs><rect width="100%" height="100%" fill="#fff"/>` +
    `<text x="${width / 2}" y="30" text-anchor="middle" font-size="19" fill="${INK}">${esc(spec.title)}</text>` +
    `<g transform="translate(0,${TITLE_H})">${svg.join('')}${front.join('')}</g></svg>`;
  return { width, height };
}
