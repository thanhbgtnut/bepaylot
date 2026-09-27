// 3D graph layout, ported from codebase-memory-mcp's src/ui/layout3d.c
// (github.com/DeusData/codebase-memory-mcp). Same strategy, run client-side
// because bepaylot's API returns plain nodes and edges:
//
//   1. seed nodes on a ring by cluster key (here: entity type) with a hashed
//      jitter, and z from the BFS depth along directed relations;
//   2. a gentle local optimisation: Barnes-Hut repulsion (octree), linear
//      edge attraction and an anchor spring that keeps nodes near their seed,
//      with a capped displacement per iteration.
//
// Colours follow the same "stellar" degree scale and sizes the same formula.

const BH_THETA = 1.2;
const OCTREE_MAX_DEPTH = 26;
const OCTREE_MIN_HALF = 1e-4;
const LOCAL_REPULSION = 8.0;
const LOCAL_ATTRACTION = 1.0;
const LOCAL_ANCHOR_K = 0.25;
const LOCAL_ITERATIONS = 40;
const Z_DEPTH_SPACING = 50.0;

export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

export interface LayoutInput {
  id: string;
  cluster: string; // nodes of a cluster start near each other on the ring
}

export function fnv1a(s: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i) & 0xff;
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h >>> 0;
}

// LCG in [-0.5, 0.5), identical to rand_float in layout3d.c.
function randFloat(seed: { v: number }): number {
  seed.v = (Math.imul(seed.v, 1103515245) + 12345) >>> 0;
  return ((seed.v >>> 16) & 0x7fff) / 32768 - 0.5;
}

// Stellar spectral colour by degree: red dwarfs (leaves) … blue giants (hubs).
export function stellarColor(degree: number): string {
  if (degree <= 1) return "#ff6050";
  if (degree <= 3) return "#ff8855";
  if (degree <= 5) return "#ffa060";
  if (degree <= 8) return "#ffc070";
  if (degree <= 12) return "#ffe080";
  if (degree <= 18) return "#fff0c0";
  if (degree <= 25) return "#fff8e8";
  if (degree <= 35) return "#e8e8ff";
  if (degree <= 50) return "#c0d0ff";
  return "#80a0ff";
}

export const STELLAR_LEGEND = [
  { color: "#80a0ff", label: "O · xanh lam", hint: "> 50 liên kết" },
  { color: "#c0d0ff", label: "B · xanh trắng", hint: "36–50" },
  { color: "#fff0c0", label: "A–F · trắng vàng", hint: "13–35" },
  { color: "#ffe080", label: "G · vàng", hint: "9–12" },
  { color: "#ffa060", label: "K · cam", hint: "2–8" },
  { color: "#ff6050", label: "M · đỏ", hint: "0–1" },
];

// base size + boost for hubs (layout3d.c: size_for_label + deg_boost).
export function nodeSize(degree: number, base = 6): number {
  return base + (degree > 5 ? Math.min(degree * 0.3, 10) : 0);
}

// ---------------------------------------------------------------- octree

interface Octree {
  cx: number;
  cy: number;
  cz: number;
  mass: number;
  half: number;
  ox: number;
  oy: number;
  oz: number;
  body: number; // body index, -1 for an internal/aggregate cell
  bodyMass: number;
  kids: (Octree | null)[];
}

const octNew = (ox: number, oy: number, oz: number, half: number): Octree => ({
  cx: 0,
  cy: 0,
  cz: 0,
  mass: 0,
  half,
  ox,
  oy,
  oz,
  body: -1,
  bodyMass: 0,
  kids: [null, null, null, null, null, null, null, null],
});

const octant = (n: Octree, x: number, y: number, z: number) => (x >= n.ox ? 1 : 0) | (y >= n.oy ? 2 : 0) | (z >= n.oz ? 4 : 0);

function child(n: Octree, o: number): Octree {
  let c = n.kids[o];
  if (!c) {
    const q = n.half * 0.5;
    c = octNew(n.ox + (o & 1 ? q : -q), n.oy + (o & 2 ? q : -q), n.oz + (o & 4 ? q : -q), q);
    n.kids[o] = c;
  }
  return c;
}

function octInsert(n: Octree, idx: number, x: number, y: number, z: number, m: number, depth: number) {
  if (n.mass === 0 && n.body === -1) {
    n.body = idx;
    n.bodyMass = m;
    n.cx = x;
    n.cy = y;
    n.cz = z;
    n.mass = m;
    return;
  }
  if (depth >= OCTREE_MAX_DEPTH || n.half < OCTREE_MIN_HALF) {
    const nm = n.mass + m;
    n.cx = (n.cx * n.mass + x * m) / nm;
    n.cy = (n.cy * n.mass + y * m) / nm;
    n.cz = (n.cz * n.mass + z * m) / nm;
    n.mass = nm;
    n.body = -1;
    return;
  }
  if (n.body >= 0) {
    const oi = n.body;
    const [bx, by, bz, bm] = [n.cx, n.cy, n.cz, n.bodyMass];
    n.body = -1;
    octInsert(child(n, octant(n, bx, by, bz)), oi, bx, by, bz, bm, depth + 1);
  }
  const nm = n.mass + m;
  n.cx = (n.cx * n.mass + x * m) / nm;
  n.cy = (n.cy * n.mass + y * m) / nm;
  n.cz = (n.cz * n.mass + z * m) / nm;
  n.mass = nm;
  octInsert(child(n, octant(n, x, y, z)), idx, x, y, z, m, depth + 1);
}

function octRepulse(n: Octree | null, px: number, py: number, pz: number, mm: number, si: number, f: Vec3) {
  if (!n || n.mass === 0 || n.body === si) return;
  const dx = px - n.cx;
  const dy = py - n.cy;
  const dz = pz - n.cz;
  let d = Math.sqrt(dx * dx + dy * dy + dz * dz);
  if (n.body >= 0 || (n.half * 2) / (d + 0.001) < BH_THETA) {
    if (d < 0.01) d = 0.01;
    const k = (LOCAL_REPULSION * mm * n.mass) / d;
    f.x += (k * dx) / d;
    f.y += (k * dy) / d;
    f.z += (k * dz) / d;
    return;
  }
  for (const c of n.kids) octRepulse(c, px, py, pz, mm, si, f);
}

// ---------------------------------------------------------------- layout

interface Body extends Vec3 {
  ax: number;
  ay: number;
  az: number;
  fx: number;
  fy: number;
  fz: number;
  mass: number;
  fixed: boolean; // kept in place (incremental layouts)
}

function localOptimize(b: Body[], es: number[], ed: number[], iterations = LOCAL_ITERATIONS) {
  const n = b.length;
  for (let iter = 0; iter < iterations; iter++) {
    let mnx = 1e9,
      mny = 1e9,
      mnz = 1e9,
      mxx = -1e9,
      mxy = -1e9,
      mxz = -1e9;
    for (const p of b) {
      p.fx = p.fy = p.fz = 0;
      mnx = Math.min(mnx, p.x);
      mny = Math.min(mny, p.y);
      mnz = Math.min(mnz, p.z);
      mxx = Math.max(mxx, p.x);
      mxy = Math.max(mxy, p.y);
      mxz = Math.max(mxz, p.z);
    }
    const half = Math.max(mxx - mnx, mxy - mny, mxz - mnz) * 0.5 + 1;
    const root = octNew((mnx + mxx) / 2, (mny + mxy) / 2, (mnz + mxz) / 2, half);
    for (let i = 0; i < n; i++) octInsert(root, i, b[i].x, b[i].y, b[i].z, b[i].mass, 0);
    const f: Vec3 = { x: 0, y: 0, z: 0 };
    for (let i = 0; i < n; i++) {
      f.x = f.y = f.z = 0;
      octRepulse(root, b[i].x, b[i].y, b[i].z, b[i].mass, i, f);
      b[i].fx += f.x;
      b[i].fy += f.y;
      b[i].fz += f.z;
    }
    for (let e = 0; e < es.length; e++) {
      const s = b[es[e]];
      const t = b[ed[e]];
      const dx = t.x - s.x;
      const dy = t.y - s.y;
      const dz = t.z - s.z;
      s.fx += dx * LOCAL_ATTRACTION;
      s.fy += dy * LOCAL_ATTRACTION;
      s.fz += dz * LOCAL_ATTRACTION;
      t.fx -= dx * LOCAL_ATTRACTION;
      t.fy -= dy * LOCAL_ATTRACTION;
      t.fz -= dz * LOCAL_ATTRACTION;
    }
    for (const p of b) {
      if (p.fixed) continue;
      p.fx += (p.ax - p.x) * LOCAL_ANCHOR_K * p.mass;
      p.fy += (p.ay - p.y) * LOCAL_ANCHOR_K * p.mass;
      p.fz += (p.az - p.z) * LOCAL_ANCHOR_K * p.mass;
      const fm = Math.sqrt(p.fx * p.fx + p.fy * p.fy + p.fz * p.fz);
      const speed = fm > 8 ? 8 / (fm + 0.001) : 1;
      p.x += p.fx * speed;
      p.y += p.fy * speed;
      p.z += p.fz * speed;
    }
  }
}

// BFS depth along directed edges from nodes without incoming edges.
function callDepth(n: number, es: number[], ed: number[]): number[] {
  const depth = new Array<number>(n).fill(-1);
  const indeg = new Array<number>(n).fill(0);
  const out: number[][] = Array.from({ length: n }, () => []);
  for (let e = 0; e < es.length; e++) {
    indeg[ed[e]]++;
    out[es[e]].push(ed[e]);
  }
  const q: number[] = [];
  for (let i = 0; i < n; i++) if (indeg[i] === 0) (depth[i] = 0), q.push(i);
  for (let h = 0; h < q.length; h++) {
    const c = q[h];
    for (const t of out[c]) if (depth[t] === -1) (depth[t] = depth[c] + 1), q.push(t);
  }
  return depth.map((d) => (d < 0 ? 0 : d));
}

export interface LayoutResult {
  pos: Map<string, Vec3>;
  degree: Map<string, number>;
}

// Lays out the graph. Nodes found in `prior` stay exactly where they are (an
// expanded graph must not jump under the user); only new nodes move. New
// nodes listed in `near` are seeded next to that existing node instead of on
// the ring.
export function layout3d(
  nodes: LayoutInput[],
  edges: { source: string; target: string }[],
  prior?: Map<string, Vec3>,
  near?: Map<string, string>,
): LayoutResult {
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  const es: number[] = [];
  const ed: number[] = [];
  const deg = new Array<number>(nodes.length).fill(0);
  for (const e of edges) {
    const s = index.get(e.source);
    const t = index.get(e.target);
    if (s === undefined || t === undefined || s === t) continue;
    es.push(s);
    ed.push(t);
    deg[s]++;
    deg[t]++;
  }
  const depth = callDepth(nodes.length, es, ed);
  // layout3d.c sizes the ring for code graphs of thousands of nodes; a small
  // knowledge graph on that ring is a few dots far apart, so the ring (not
  // the local forces) shrinks with the node count.
  const ring = Math.min(1, Math.max(0.2, Math.sqrt(nodes.length) / 45));

  const bodies: Body[] = nodes.map((n, i) => {
    let p: Vec3;
    const had = prior?.get(n.id);
    const anchor = near?.get(n.id);
    const seed = { v: fnv1a(n.id) };
    if (had) p = had;
    else if (anchor && prior?.get(anchor)) {
      const a = prior.get(anchor)!;
      p = { x: a.x + randFloat(seed) * 80, y: a.y + randFloat(seed) * 80, z: a.z + randFloat(seed) * 80 };
    } else {
      const h = fnv1a(n.cluster);
      const angle = ((h & 0xffff) / 65535) * 6.2832;
      const r = (500 + (((h >>> 16) & 0xff) / 255) * 250) * ring;
      p = { x: r * Math.cos(angle) + randFloat(seed) * 40, y: r * Math.sin(angle) + randFloat(seed) * 40, z: -depth[i] * Z_DEPTH_SPACING };
    }
    return { ...p, ax: p.x, ay: p.y, az: p.z, fx: 0, fy: 0, fz: 0, mass: deg[i] + 1, fixed: !!had };
  });

  localOptimize(bodies, es, ed, nodes.length > 100_000 ? 20 : LOCAL_ITERATIONS);

  const pos = new Map<string, Vec3>();
  const degree = new Map<string, number>();
  nodes.forEach((n, i) => {
    pos.set(n.id, { x: bodies[i].x, y: bodies[i].y, z: bodies[i].z });
    degree.set(n.id, deg[i]);
  });
  return { pos, degree };
}
