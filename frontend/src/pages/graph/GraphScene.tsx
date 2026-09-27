// Three.js scene for the knowledge graph, ported from codebase-memory-mcp's
// graph-ui (GraphScene / NodeCloud / EdgeLines / NodeLabels / NodeTooltip):
// instanced glowing spheres, additive edge lines, sprite labels, bloom,
// orbit controls with a fly-to camera and idle auto-rotation.

import { Html, OrbitControls } from "@react-three/drei";
import { Canvas, useFrame, useThree, type ThreeEvent } from "@react-three/fiber";
import { Bloom, EffectComposer } from "@react-three/postprocessing";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import type { OrbitControls as OrbitControlsImpl } from "three-stdlib";

import { bloomIntensityScale, DEFAULT_DISPLAY, edgeIntensityScale, nodeBoostScale, nodeGlowBoost, type DisplaySettings } from "./density";

export interface GNode {
  id: string;
  x: number;
  y: number;
  z: number;
  name: string;
  type: string;
  size: number;
  color: string;
  degree: number;
}
export interface GEdge {
  id: string;
  source: string;
  target: string;
  type: string;
}
export interface CameraTarget {
  position: THREE.Vector3;
  lookAt: THREE.Vector3;
}

const BG = "#06090f";
const BASE_BLOOM_INTENSITY = 1.45;

// Relation type → colour (codebase-memory's edge palette, cycled).
const EDGE_PALETTE = ["#1DA27E", "#3b82f6", "#a855f7", "#22c55e", "#eab308", "#f97316", "#e11d48", "#ec4899", "#f59e0b", "#e879f9", "#06b6d4", "#64748b"];
const edgeColorCache = new Map<string, string>();
export function edgeColor(type: string): string {
  let c = edgeColorCache.get(type);
  if (!c) {
    c = EDGE_PALETTE[edgeColorCache.size % EDGE_PALETTE.length];
    edgeColorCache.set(type, c);
  }
  return c;
}

// ---------------------------------------------------------------- nodes

function nodeRGB(n: GNode, hl: Set<string> | null, boost: number, tmp: THREE.Color): [number, number, number] {
  tmp.set(n.color);
  if (hl && hl.size > 0 && !hl.has(n.id)) tmp.multiplyScalar(0.15);
  else tmp.multiplyScalar(1 + (nodeGlowBoost(tmp.r, tmp.g, tmp.b) - 1) * boost);
  return [tmp.r, tmp.g, tmp.b];
}

function NodeCloud({
  nodes,
  highlighted,
  boost,
  onHover,
  onClick,
  onDoubleClick,
}: {
  nodes: GNode[];
  highlighted: Set<string> | null;
  boost: number;
  onHover: (n: GNode | null) => void;
  onClick: (n: GNode, e: ThreeEvent<MouseEvent>) => void;
  onDoubleClick: (n: GNode) => void;
}) {
  const mesh = useRef<THREE.InstancedMesh>(null);
  const tmpObj = useMemo(() => new THREE.Object3D(), []);
  const tmpColor = useMemo(() => new THREE.Color(), []);
  const detail: [number, number, number] = nodes.length <= 8000 ? [1, 32, 24] : [1, 16, 12];

  const colors = useMemo(() => {
    const arr = new Float32Array(nodes.length * 3);
    nodes.forEach((n, i) => arr.set(nodeRGB(n, highlighted, boost, tmpColor), i * 3));
    return arr;
  }, [nodes, highlighted, boost, tmpColor]);

  useEffect(() => {
    const m = mesh.current;
    if (!m) return;
    const has = highlighted && highlighted.size > 0;
    nodes.forEach((n, i) => {
      tmpObj.position.set(n.x, n.y, n.z);
      const s = n.size * (!has || highlighted.has(n.id) ? 0.5 : 0.2);
      tmpObj.scale.set(s, s, s);
      tmpObj.updateMatrix();
      m.setMatrixAt(i, tmpObj.matrix);
    });
    m.instanceMatrix.needsUpdate = true;
    m.computeBoundingSphere();
  }, [nodes, highlighted, tmpObj]);

  const pick = (e: ThreeEvent<MouseEvent | PointerEvent>) => (e.instanceId !== undefined && e.instanceId < nodes.length ? nodes[e.instanceId] : null);

  return (
    <instancedMesh
      key={nodes.length}
      ref={mesh}
      args={[undefined, undefined, nodes.length]}
      frustumCulled={false}
      onPointerOver={(e) => {
        e.stopPropagation();
        onHover(pick(e));
        document.body.style.cursor = "pointer";
      }}
      onPointerOut={() => {
        onHover(null);
        document.body.style.cursor = "";
      }}
      onClick={(e) => {
        e.stopPropagation();
        const n = pick(e);
        if (n) onClick(n, e);
      }}
      onDoubleClick={(e) => {
        e.stopPropagation();
        const n = pick(e);
        if (n) onDoubleClick(n);
      }}
    >
      <sphereGeometry args={detail} />
      <meshBasicMaterial vertexColors toneMapped={false} />
      <instancedBufferAttribute attach="geometry-attributes-color" args={[colors, 3]} />
    </instancedMesh>
  );
}

// ---------------------------------------------------------------- edges

function EdgeLines({ nodes, edges, highlighted, brightness }: { nodes: GNode[]; edges: GEdge[]; highlighted: Set<string> | null; brightness: number }) {
  const geometry = useMemo(() => {
    const scale = edgeIntensityScale(edges.length) * brightness;
    const byId = new Map(nodes.map((n) => [n.id, n]));
    const has = highlighted && highlighted.size > 0;
    const pos = new Float32Array(edges.length * 6);
    const col = new Float32Array(edges.length * 6);
    const c = new THREE.Color();
    let k = 0;
    for (const e of edges) {
      const s = byId.get(e.source);
      const t = byId.get(e.target);
      if (!s || !t) continue;
      const sh = !has || highlighted.has(s.id);
      const th = !has || highlighted.has(t.id);
      if (has && !sh && !th) continue;
      // Same-cluster edges read as structure, cross-cluster ones as faint links.
      let intensity = s.type === t.type ? 0.25 : 0.08;
      intensity = has ? (sh && th ? 0.6 : 0.04 * scale) : intensity * scale;
      pos.set([s.x, s.y, s.z, t.x, t.y, t.z], k * 6);
      c.set(edgeColor(e.type));
      col.set([c.r * intensity, c.g * intensity, c.b * intensity, c.r * intensity, c.g * intensity, c.b * intensity], k * 6);
      k++;
    }
    const g = new THREE.BufferGeometry();
    g.setAttribute("position", new THREE.BufferAttribute(pos.slice(0, k * 6), 3));
    g.setAttribute("color", new THREE.BufferAttribute(col.slice(0, k * 6), 3));
    return g;
  }, [nodes, edges, highlighted, brightness]);
  useEffect(() => () => geometry.dispose(), [geometry]);
  return (
    <lineSegments geometry={geometry}>
      <lineBasicMaterial vertexColors transparent blending={THREE.AdditiveBlending} depthWrite={false} toneMapped={false} />
    </lineSegments>
  );
}

// ---------------------------------------------------------------- labels

const FONT_PX = 64;
const FONT = `500 ${FONT_PX}px "Google Sans Variable", Roboto, system-ui, sans-serif`;

function labelTexture(text: string, color: string, pill = false) {
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  ctx.font = FONT;
  let t = text;
  while (ctx.measureText(t).width > 720 && t.length > 2) t = t.slice(0, -2);
  if (t !== text) t = t.trimEnd() + "…";
  const w = Math.ceil(ctx.measureText(t).width) + 64;
  const h = FONT_PX + 44;
  const ratio = Math.min(window.devicePixelRatio || 1, 2);
  canvas.width = Math.ceil(w * ratio);
  canvas.height = Math.ceil(h * ratio);
  ctx.scale(ratio, ratio);
  ctx.font = FONT;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  if (pill) {
    // Edge labels sit on a dark pill so they stay readable over bright lines.
    const r = h / 2 - 6;
    ctx.fillStyle = "rgba(10,14,22,0.88)";
    ctx.strokeStyle = color;
    ctx.lineWidth = 4;
    ctx.beginPath();
    ctx.roundRect(4, 6, w - 8, h - 12, r);
    ctx.fill();
    ctx.stroke();
    ctx.fillStyle = color;
    ctx.fillText(t, w / 2, h / 2 + 2);
  } else {
    ctx.lineJoin = "round";
    ctx.lineWidth = 8;
    ctx.strokeStyle = "rgba(0,0,0,0.9)";
    ctx.fillStyle = color;
    ctx.strokeText(t, w / 2, h / 2);
    ctx.fillText(t, w / 2, h / 2);
  }
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.minFilter = THREE.LinearFilter;
  tex.generateMipmaps = false;
  return { tex, w, h };
}

// Labels keep a constant size on screen (sizeAttenuation off), like map
// labels: readable when the whole graph is in view and when zoomed in.
// `h` is the sprite height as a fraction of the viewport height.
const NODE_LABEL_H = 0.024;

function Label({ node }: { node: GNode }) {
  const label = useMemo(() => labelTexture(node.name, node.color), [node.name, node.color]);
  useEffect(() => () => label?.tex.dispose(), [label]);
  if (!label) return null;
  const h = NODE_LABEL_H * (label.h / (FONT_PX + 44));
  const w = h * (label.w / label.h);
  return (
    <sprite position={[node.x, node.y, node.z]} center={[0.5, -0.45]} scale={[w, h, 1]} renderOrder={20} frustumCulled={false}>
      <spriteMaterial map={label.tex} transparent depthWrite={false} toneMapped={false} sizeAttenuation={false} />
    </sprite>
  );
}

function NodeLabels({ nodes, highlighted, max = 80 }: { nodes: GNode[]; highlighted: Set<string> | null; max?: number }) {
  const shown = useMemo(() => {
    const pool = highlighted && highlighted.size > 0 ? nodes.filter((n) => highlighted.has(n.id)) : nodes;
    return [...pool].sort((a, b) => b.size - a.size).slice(0, max);
  }, [nodes, highlighted, max]);
  return (
    <group>
      {shown.map((n) => (
        <Label key={n.id} node={n} />
      ))}
    </group>
  );
}

// ---------------------------------------------------------------- relations on edges

interface PairEdge {
  key: string;
  s: GNode;
  t: GNode;
  types: string[];
}

// One entry per node pair, relation types merged ("WORKS_FOR · OWNS").
function pairEdges(nodes: GNode[], edges: GEdge[]): PairEdge[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const pairs = new Map<string, PairEdge>();
  for (const e of edges) {
    const s = byId.get(e.source);
    const t = byId.get(e.target);
    if (!s || !t) continue;
    const key = [e.source, e.target].sort().join("|");
    const p = pairs.get(key) ?? { key, s, t, types: [] };
    if (!p.types.includes(e.type)) p.types.push(e.type);
    pairs.set(key, p);
  }
  return [...pairs.values()];
}

// Relation names are plain DOM labels laid over the canvas (not sprites):
// crisp at any zoom, not blurred by bloom, and part of the normal React tree
// (no extra roots). A small component inside the canvas projects each edge
// midpoint every frame and moves its label through a ref.
function edgeLabelPairs(nodes: GNode[], edges: GEdge[], highlighted: Set<string> | null, all: boolean, max = 200): PairEdge[] {
  const pairs = pairEdges(nodes, edges);
  if (highlighted && highlighted.size > 0) return pairs.filter((p) => highlighted.has(p.s.id) && highlighted.has(p.t.id)).slice(0, max);
  return all ? pairs.slice(0, max) : [];
}

function EdgeLabelProjector({ pairs, refs }: { pairs: PairEdge[]; refs: React.RefObject<(HTMLSpanElement | null)[]> }) {
  const v = useMemo(() => new THREE.Vector3(), []);
  useFrame(({ camera, size }) => {
    pairs.forEach((p, i) => {
      const el = refs.current?.[i];
      if (!el) return;
      v.set((p.s.x + p.t.x) / 2, (p.s.y + p.t.y) / 2, (p.s.z + p.t.z) / 2).project(camera);
      const visible = v.z < 1 && Math.abs(v.x) <= 1.05 && Math.abs(v.y) <= 1.05;
      el.style.display = visible ? "" : "none";
      if (visible) el.style.transform = `translate(-50%, -50%) translate(${((v.x + 1) / 2) * size.width}px, ${((1 - v.y) / 2) * size.height}px)`;
    });
  });
  return null;
}

function EdgeLabelLayer({ pairs, refs }: { pairs: PairEdge[]; refs: React.RefObject<(HTMLSpanElement | null)[]> }) {
  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden">
      {pairs.map((p, i) => {
        const color = edgeColor(p.types[0]);
        return (
          <span
            key={p.key + p.types.join()}
            ref={(el) => {
              refs.current[i] = el;
            }}
            className="absolute top-0 left-0 rounded-full border bg-[#0a0e16]/85 px-1.5 py-px font-mono text-[10px] leading-4 font-medium whitespace-nowrap will-change-transform"
            style={{ color, borderColor: color + "99", display: "none" }}
          >
            {p.types.join(" · ")}
          </span>
        );
      })}
    </div>
  );
}

// Direction cones near the target end of every edge (one instanced mesh).
function EdgeArrows({ nodes, edges, highlighted }: { nodes: GNode[]; edges: GEdge[]; highlighted: Set<string> | null }) {
  const mesh = useRef<THREE.InstancedMesh>(null);
  const list = useMemo(() => {
    const byId = new Map(nodes.map((n) => [n.id, n]));
    const seen = new Set<string>();
    const out: { s: GNode; t: GNode; type: string }[] = [];
    for (const e of edges) {
      const s = byId.get(e.source);
      const t = byId.get(e.target);
      const k = e.source + ">" + e.target;
      if (!s || !t || seen.has(k)) continue;
      seen.add(k);
      out.push({ s, t, type: e.type });
    }
    return out;
  }, [nodes, edges]);
  const colors = useMemo(() => {
    const has = highlighted && highlighted.size > 0;
    const c = new THREE.Color();
    const arr = new Float32Array(list.length * 3);
    list.forEach((a, i) => {
      const lit = !has || (highlighted.has(a.s.id) && highlighted.has(a.t.id));
      c.set(edgeColor(a.type)).multiplyScalar(lit ? (has ? 1.6 : 0.9) : 0.08);
      arr.set([c.r, c.g, c.b], i * 3);
    });
    return arr;
  }, [list, highlighted]);
  useEffect(() => {
    const m = mesh.current;
    if (!m) return;
    const o = new THREE.Object3D();
    const up = new THREE.Vector3(0, 1, 0);
    const dir = new THREE.Vector3();
    list.forEach((a, i) => {
      dir.set(a.t.x - a.s.x, a.t.y - a.s.y, a.t.z - a.s.z);
      const len = dir.length() || 1;
      dir.divideScalar(len);
      const back = a.t.size * 0.5 + 2.2; // stop just outside the target sphere
      o.position.set(a.t.x - dir.x * back, a.t.y - dir.y * back, a.t.z - dir.z * back);
      o.quaternion.setFromUnitVectors(up, dir);
      const k = Math.min(1.6, Math.max(0.8, len / 60));
      o.scale.set(k, k, k);
      o.updateMatrix();
      m.setMatrixAt(i, o.matrix);
    });
    m.instanceMatrix.needsUpdate = true;
    m.computeBoundingSphere();
  }, [list]);
  if (!list.length) return null;
  return (
    <instancedMesh key={list.length} ref={mesh} args={[undefined, undefined, list.length]} frustumCulled={false} raycast={() => null}>
      <coneGeometry args={[1.1, 3.2, 10]} />
      <meshBasicMaterial vertexColors toneMapped={false} />
      <instancedBufferAttribute attach="geometry-attributes-color" args={[colors, 3]} />
    </instancedMesh>
  );
}

function Tooltip({ node, typeColor }: { node: GNode; typeColor: (t: string) => string }) {
  return (
    <Html position={[node.x, node.y + node.size * 0.7, node.z]} center style={{ pointerEvents: "none" }}>
      <div className="max-w-85 rounded-lg border border-white/10 bg-[#1a1a2e]/95 px-3 py-2 text-xs whitespace-nowrap text-white shadow-xl backdrop-blur">
        <div className="flex items-center gap-1.5">
          <span className="size-2 flex-none rounded-full" style={{ background: typeColor(node.type) }} />
          <span className="truncate font-medium">{node.name}</span>
          <span className="ml-1 flex-none text-white/40">{node.type}</span>
        </div>
        <div className="mt-1 text-white/40">
          {node.degree} liên kết · click: chi tiết · shift+click: mở rộng · double-click: lấy làm tâm
        </div>
      </div>
    </Html>
  );
}

// ---------------------------------------------------------------- camera

function CameraAnimator({ target, controls }: { target: CameraTarget | null; controls: React.RefObject<OrbitControlsImpl | null> }) {
  const { camera } = useThree();
  const tgt = useRef<CameraTarget | null>(null);
  const progress = useRef(1);
  useEffect(() => {
    if (target) {
      tgt.current = target;
      progress.current = 0;
    }
  }, [target]);
  useFrame(() => {
    if (!tgt.current || progress.current >= 1) return;
    progress.current = Math.min(1, progress.current + 0.02);
    const t = 1 - Math.pow(1 - progress.current, 3);
    camera.position.lerp(tgt.current.position, t * 0.08);
    const c = controls.current;
    if (c) {
      c.target.lerp(tgt.current.lookAt, t * 0.08);
      c.update();
    } else camera.lookAt(tgt.current.lookAt);
  });
  return null;
}

function IdleAutoRotate({ controls, after = 60_000 }: { controls: React.RefObject<OrbitControlsImpl | null>; after?: number }) {
  const { gl } = useThree();
  const last = useRef(Date.now());
  const reset = useCallback(() => {
    last.current = Date.now();
    if (controls.current) controls.current.autoRotate = false;
  }, [controls]);
  useEffect(() => {
    const el = gl.domElement;
    el.addEventListener("pointerdown", reset);
    el.addEventListener("wheel", reset);
    return () => {
      el.removeEventListener("pointerdown", reset);
      el.removeEventListener("wheel", reset);
    };
  }, [gl, reset]);
  useFrame(() => {
    if (controls.current) controls.current.autoRotate = Date.now() - last.current > after;
  });
  return null;
}

// Camera position framing a set of nodes (computeCameraTarget).
// `spreadFactor` 3 frames a selection loosely (codebase-memory's default);
// framing the whole graph uses a tighter factor.
export function cameraTargetFor(nodes: GNode[], ids: Set<string>, spreadFactor = 3): CameraTarget | null {
  const sel = nodes.filter((n) => ids.has(n.id));
  if (!sel.length) return null;
  const c = sel.reduce((a, n) => ({ x: a.x + n.x / sel.length, y: a.y + n.y / sel.length, z: a.z + n.z / sel.length }), { x: 0, y: 0, z: 0 });
  const spread = Math.max(0, ...sel.map((n) => Math.hypot(n.x - c.x, n.y - c.y, n.z - c.z)));
  // codebase-memory keeps ≥ 200–300 units for code graphs; a handful of
  // entities needs the camera closer to stay legible.
  const d = Math.max(sel.length <= 2 ? 120 : 140, spread * spreadFactor);
  return { lookAt: new THREE.Vector3(c.x, c.y, c.z), position: new THREE.Vector3(c.x + d * 0.2, c.y + d * 0.15, c.z + d) };
}

// ---------------------------------------------------------------- scene

export function GraphScene({
  nodes,
  edges,
  highlighted,
  cameraTarget,
  showLabels,
  showEdgeLabels = false,
  display = DEFAULT_DISPLAY,
  typeColor,
  onNodeClick,
  onNodeDoubleClick,
  onBackgroundClick,
}: {
  nodes: GNode[];
  edges: GEdge[];
  highlighted: Set<string> | null;
  cameraTarget: CameraTarget | null;
  showLabels: boolean;
  showEdgeLabels?: boolean;
  display?: DisplaySettings;
  typeColor: (t: string) => string;
  onNodeClick: (n: GNode, shift: boolean) => void;
  onNodeDoubleClick: (n: GNode) => void;
  onBackgroundClick?: () => void;
}) {
  const [hovered, setHovered] = useState<GNode | null>(null);
  const controls = useRef<OrbitControlsImpl | null>(null);
  const labelPairs = useMemo(() => edgeLabelPairs(nodes, edges, highlighted, showEdgeLabels), [nodes, edges, highlighted, showEdgeLabels]);
  const labelRefs = useRef<(HTMLSpanElement | null)[]>([]);
  const boost = nodeBoostScale(nodes.length) * display.nodeGlow;
  const bloom = BASE_BLOOM_INTENSITY * bloomIntensityScale(nodes.length) * display.bloom;

  return (
    <div className="relative size-full">
    <Canvas
      camera={{ position: [0, 0, 1400], fov: 50, near: 0.1, far: 100000 }}
      style={{ background: BG }}
      dpr={[1, 1.5]}
      gl={{ antialias: false, alpha: false, powerPreference: "high-performance" }}
      onPointerMissed={onBackgroundClick}
    >
      <color attach="background" args={[BG]} />
      <ambientLight intensity={0.5} />
      <pointLight position={[500, 500, 500]} intensity={0.6} />
      <pointLight position={[-300, -200, -300]} intensity={0.4} color="#6040ff" />

      <EdgeLines nodes={nodes} edges={edges} highlighted={highlighted} brightness={display.edgeBrightness} />
      <NodeCloud nodes={nodes} highlighted={highlighted} boost={boost} onHover={setHovered} onClick={(n, e) => onNodeClick(n, e.nativeEvent.shiftKey)} onDoubleClick={onNodeDoubleClick} />
      <EdgeArrows nodes={nodes} edges={edges} highlighted={highlighted} />
      {showLabels && <NodeLabels nodes={nodes} highlighted={highlighted} />}
      <EdgeLabelProjector pairs={labelPairs} refs={labelRefs} />
      {hovered && <Tooltip node={hovered} typeColor={typeColor} />}

      <CameraAnimator target={cameraTarget} controls={controls} />
      <IdleAutoRotate controls={controls} />

      <EffectComposer multisampling={0}>
        <Bloom luminanceThreshold={0.3} luminanceSmoothing={0.7} intensity={bloom} mipmapBlur radius={0.6} />
      </EffectComposer>
      <OrbitControls ref={controls} enableDamping dampingFactor={0.08} rotateSpeed={0.5} zoomSpeed={1.5} minDistance={10} maxDistance={50000} autoRotateSpeed={0.4} />
    </Canvas>
    <EdgeLabelLayer pairs={labelPairs} refs={labelRefs} />
    </div>
  );
}
