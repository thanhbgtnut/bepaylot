// Visual density compensation and display settings, from codebase-memory-mcp
// graph-ui/src/lib/density.ts: edges blend additively, so they are dimmed as
// their count grows; node glow and bloom stay bright so hubs still shine.

export const EDGE_REFERENCE_COUNT = 2500;
const EDGE_MIN_SCALE = 0.05;
export function edgeIntensityScale(edgeCount: number): number {
  if (edgeCount <= EDGE_REFERENCE_COUNT) return 1;
  return Math.max(EDGE_MIN_SCALE, Math.sqrt(EDGE_REFERENCE_COUNT / edgeCount));
}

const NODE_REFERENCE_COUNT = 25000;
const NODE_FADE_END = 250000;
const BLOOM_FLOOR = 0.7;
const NODE_BOOST_FLOOR = 0.8;
function fadeFactor(n: number) {
  if (n <= NODE_REFERENCE_COUNT) return 0;
  return Math.min(1, (n - NODE_REFERENCE_COUNT) / (NODE_FADE_END - NODE_REFERENCE_COUNT));
}
export const bloomIntensityScale = (n: number) => 1 - fadeFactor(n) * (1 - BLOOM_FLOOR);
export const nodeBoostScale = (n: number) => 1 - fadeFactor(n) * (1 - NODE_BOOST_FLOOR);

// Colour-aware glow: blue hubs glow most, red leaves modestly, white/yellow
// least (they bloom on their own).
export function nodeGlowBoost(r: number, g: number, b: number): number {
  const blueness = Math.max(0, b - Math.max(r, g));
  const redness = Math.max(0, r - Math.max(g, b));
  return 1.35 + blueness * 2.4 + redness * 0.9;
}

export interface DisplaySettings {
  edgeBrightness: number; // 0.1–3
  nodeGlow: number; // 0–2
  bloom: number; // 0–2
}
export const DEFAULT_DISPLAY: DisplaySettings = { edgeBrightness: 1, nodeGlow: 1, bloom: 1 };
export const DISPLAY_LIMITS = {
  edgeBrightness: { min: 0.1, max: 3 },
  nodeGlow: { min: 0, max: 2 },
  bloom: { min: 0, max: 2 },
} as const;

const KEY = "bp.graphDisplay";
export function loadDisplay(): DisplaySettings {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? "null");
    if (raw) {
      const out = { ...DEFAULT_DISPLAY };
      for (const k of Object.keys(DISPLAY_LIMITS) as (keyof DisplaySettings)[]) {
        const v = Number(raw[k]);
        if (Number.isFinite(v)) out[k] = Math.min(DISPLAY_LIMITS[k].max, Math.max(DISPLAY_LIMITS[k].min, v));
      }
      return out;
    }
  } catch {
    /* ignore */
  }
  return DEFAULT_DISPLAY;
}
export function saveDisplay(d: DisplaySettings) {
  try {
    localStorage.setItem(KEY, JSON.stringify(d));
  } catch {
    /* ignore */
  }
}
