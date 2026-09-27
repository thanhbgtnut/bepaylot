import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { getWikiPage, wikiGraph } from "../../api/endpoints";
import type { WikiGraph, WikiPage } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { CasePicker } from "../../components/CasePicker";
import { NeedKB, TopBar } from "../../components/Layout";
import { useToast } from "../../components/toast";
import { Empty, Icon, Loading, Spinner } from "../../components/ui";
import { metaValue } from "../../lib/format";
import { DEFAULT_DISPLAY, DISPLAY_LIMITS, loadDisplay, saveDisplay, type DisplaySettings } from "./density";
import { cameraTargetFor, edgeColor, GraphScene, type CameraTarget, type GEdge, type GNode } from "./GraphScene";
import { layout3d, nodeSize, STELLAR_LEGEND, stellarColor } from "./layout3d";

// The link graph of one case wiki: every wiki page is a node, every link
// ([[slug]] or a typed relation of the schema) an edge. A case wiki is
// small, so the whole graph is loaded and laid out at once.

const KIND_LABEL: Record<string, string> = { overview: "tổng quan", source: "tài liệu", entity: "thực thể", topic: "chủ đề", note: "ghi chú" };
const nodeType = (n: { kind: string; entity_type?: string }) => (n.kind === "entity" ? n.entity_type || "thực thể" : KIND_LABEL[n.kind] ?? n.kind);

const TYPE_PALETTE = ["#5b8def", "#2fb67c", "#e0a72e", "#d9594c", "#9b6bf2", "#2bb3c9", "#e45f9c", "#ef8a3a", "#8a94a6", "#6cc07a"];
const typeColors = new Map<string, string>();
const typeColor = (t: string) => {
  if (!typeColors.has(t)) typeColors.set(t, TYPE_PALETTE[typeColors.size % TYPE_PALETTE.length]);
  return typeColors.get(t)!;
};
type ColorMode = "stellar" | "type";

const glass = "rounded-xl border border-white/10 bg-[#11151c]/85 text-[#e3e3e3] shadow-2xl backdrop-blur-md";
const darkBtn = "inline-flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-[13px] text-[#c4c7c5] hover:bg-white/10 hover:text-white disabled:opacity-40";
const darkBtnOn = "bg-[#a8c7fa]/20 text-[#a8c7fa] hover:bg-[#a8c7fa]/25 hover:text-[#a8c7fa]";

// Shortest path over undirected links (BFS).
function shortestPath(edges: GEdge[], from: string, to: string): string[] | null {
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    adj.set(e.source, [...(adj.get(e.source) ?? []), e.target]);
    adj.set(e.target, [...(adj.get(e.target) ?? []), e.source]);
  }
  const prev = new Map<string, string>([[from, ""]]);
  const queue = [from];
  while (queue.length) {
    const cur = queue.shift()!;
    if (cur === to) break;
    for (const n of adj.get(cur) ?? []) if (!prev.has(n)) (prev.set(n, cur), queue.push(n));
  }
  if (!prev.has(to)) return null;
  const out = [to];
  while (out[out.length - 1] !== from) out.push(prev.get(out[out.length - 1])!);
  return out.reverse();
}

export function GraphPage() {
  const { kb, kcase, cases, selectCase } = useApp();
  const { toast } = useToast();
  const [sp, setSp] = useSearchParams();
  const caseId = sp.get("case") || kcase?.id || "";
  const c = cases?.find((x) => x.id === caseId) ?? null;

  const [data, setData] = useState<WikiGraph | null>(null);
  const [err, setErr] = useState("");
  const [selected, setSelected] = useState<string | null>(sp.get("page"));
  const [path, setPath] = useState<Set<string> | null>(null);
  const [pickingPath, setPickingPath] = useState<string | null>(null);
  const [camera, setCamera] = useState<CameraTarget | null>(null);
  const [hiddenTypes, setHiddenTypes] = useState<Set<string>>(new Set());
  const [hiddenRels, setHiddenRels] = useState<Set<string>>(new Set());
  const [labels, setLabels] = useState(true);
  const [edgeLabels, setEdgeLabels] = useState<boolean | null>(null);
  const [colorMode, setColorMode] = useState<ColorMode>("type");
  const [display, setDisplayState] = useState<DisplaySettings>(loadDisplay);
  const [panel, setPanel] = useState<"filters" | "display" | null>("filters");
  const setDisplay = (d: DisplaySettings) => {
    setDisplayState(d);
    saveDisplay(d);
  };

  useEffect(() => {
    if (caseId && caseId !== kcase?.id && cases?.some((x) => x.id === caseId)) selectCase(caseId);
  }, [caseId, cases]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!caseId) return;
    let alive = true;
    setData(null);
    setErr("");
    wikiGraph(caseId).then(
      (g) => alive && setData(g),
      (e) => alive && setErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [caseId]);
  useEffect(() => {
    const next = new URLSearchParams(sp);
    if (caseId) next.set("case", caseId);
    if (selected) next.set("page", selected);
    else next.delete("page");
    if (next.toString() !== sp.toString()) setSp(next, { replace: true });
  }, [selected, caseId]); // eslint-disable-line react-hooks/exhaustive-deps

  const graph = useMemo(() => {
    if (!data) return { nodes: [] as GNode[], edges: [] as GEdge[] };
    const edges: GEdge[] = (data.edges ?? []).map((e, i) => ({ id: String(i), source: e.from, target: e.to, type: e.relation || "liên kết" }));
    const { pos, degree } = layout3d(
      (data.nodes ?? []).map((n) => ({ id: n.slug, cluster: nodeType(n) })),
      edges,
    );
    const nodes: GNode[] = (data.nodes ?? []).map((n) => {
      const p = pos.get(n.slug)!;
      const d = degree.get(n.slug) ?? 0;
      return { id: n.slug, x: p.x, y: p.y, z: p.z, name: n.title, type: nodeType(n), degree: d, size: nodeSize(d), color: stellarColor(d) };
    });
    return { nodes, edges };
  }, [data]);

  const visible = useMemo(() => {
    const nodes = graph.nodes.filter((n) => !hiddenTypes.has(n.type)).map((n) => (colorMode === "type" ? { ...n, color: typeColor(n.type) } : n));
    const ids = new Set(nodes.map((n) => n.id));
    const edges = graph.edges.filter((e) => ids.has(e.source) && ids.has(e.target) && !hiddenRels.has(e.type));
    return { nodes, edges };
  }, [graph, hiddenTypes, hiddenRels, colorMode]);

  const highlighted = useMemo(() => {
    if (path) return path;
    if (!selected) return null;
    const s = new Set([selected]);
    for (const e of visible.edges) {
      if (e.source === selected) s.add(e.target);
      if (e.target === selected) s.add(e.source);
    }
    return s;
  }, [selected, visible.edges, path]);

  // Frame the whole graph once it is laid out, then fly to the selection.
  useEffect(() => {
    if (graph.nodes.length) setCamera(cameraTargetFor(graph.nodes, new Set(graph.nodes.map((n) => n.id)), 1.8));
  }, [graph]);
  useEffect(() => {
    if (highlighted) setCamera(cameraTargetFor(visible.nodes, highlighted));
  }, [highlighted]); // eslint-disable-line react-hooks/exhaustive-deps

  const counts = useMemo(() => {
    const types = new Map<string, number>();
    const rels = new Map<string, number>();
    for (const n of graph.nodes) types.set(n.type, (types.get(n.type) ?? 0) + 1);
    for (const e of graph.edges) rels.set(e.type, (rels.get(e.type) ?? 0) + 1);
    const sort = (m: Map<string, number>) => [...m.entries()].sort((a, b) => b[1] - a[1]);
    return { types: sort(types), rels: sort(rels) };
  }, [graph]);

  const onNodeClick = (n: GNode) => {
    if (pickingPath) {
      const from = pickingPath;
      setPickingPath(null);
      if (from === n.id) return;
      const p = shortestPath(visible.edges, from, n.id);
      if (!p) return toast("Không có đường nối hai trang này", "err");
      setPath(new Set(p));
      setSelected(null);
      toast(`Đường đi: ${p.length - 1} bước`);
      return;
    }
    setPath(null);
    setSelected(n.id);
  };
  const toggle = (set: Set<string>, v: string) => {
    const n = new Set(set);
    if (n.has(v)) n.delete(v);
    else n.add(v);
    return n;
  };

  if (!kb)
    return (
      <>
        <TopBar title="Graph" />
        <NeedKB />
      </>
    );

  const edgeLabelsOn = edgeLabels ?? visible.edges.length <= 150;
  return (
    <>
      <TopBar title="Graph hồ sơ" subtitle={data ? `${graph.nodes.length} trang · ${graph.edges.length} liên kết` : undefined}>
        <CasePicker
          value={c}
          onChange={(x) => {
            setSelected(null);
            setPath(null);
            setSp({ case: x.id }, { replace: true });
          }}
        />
      </TopBar>
      {cases && !cases.length ? (
        <Empty icon="folder_off" title="Chưa có hồ sơ">
          Tải file lên kèm mã hồ sơ để tạo hồ sơ đầu tiên.
        </Empty>
      ) : err ? (
        <div className="p-6 text-err">{err}</div>
      ) : !data ? (
        <Loading />
      ) : !graph.nodes.length ? (
        <Empty icon="hub" title="Wiki của hồ sơ chưa có trang">
          Graph là các trang wiki (nút) và liên kết giữa chúng (cạnh). Wiki được dựng tự động khi file của hồ sơ xử lý xong.
          <div>
            <Link className="btn btn-primary mt-5" to={`/wiki/${caseId}`}>
              <Icon name="menu_book" size={18} /> Mở wiki
            </Link>
          </div>
        </Empty>
      ) : (
        <div className="relative min-h-0 flex-1 overflow-hidden bg-[#06090f]">
          <GraphScene
            nodes={visible.nodes}
            edges={visible.edges}
            highlighted={highlighted}
            cameraTarget={camera}
            showLabels={labels}
            showEdgeLabels={edgeLabelsOn}
            display={display}
            typeColor={typeColor}
            onNodeClick={onNodeClick}
            onNodeDoubleClick={(n) => setSelected(n.id)}
            onBackgroundClick={() => {
              setSelected(null);
              setPath(null);
            }}
          />

          <div className={glass + " absolute top-3 left-3 z-10 flex w-85 flex-col gap-2 p-2"}>
            <NodeSearch nodes={graph.nodes} onPick={(id) => (setPath(null), setSelected(id))} />
            <div className="flex flex-wrap items-center gap-1">
              <button className={darkBtn + (labels ? " " + darkBtnOn : "")} onClick={() => setLabels((l) => !l)} title="Tên trang trên nút">
                <Icon name="label" size={18} />
              </button>
              <button className={darkBtn + (edgeLabelsOn ? " " + darkBtnOn : "")} onClick={() => setEdgeLabels(!edgeLabelsOn)} title="Tên quan hệ trên mọi cạnh">
                <Icon name="conversion_path" size={18} /> Quan hệ
              </button>
              <button className={darkBtn + (colorMode === "type" ? " " + darkBtnOn : "")} onClick={() => setColorMode((m) => (m === "stellar" ? "type" : "stellar"))} title="Đổi cách tô màu">
                <Icon name="palette" size={18} /> {colorMode === "stellar" ? "Liên kết" : "Loại"}
              </button>
              <button
                className={darkBtn + (pickingPath ? " " + darkBtnOn : "")}
                onClick={() => {
                  if (pickingPath) return setPickingPath(null);
                  if (!selected) return toast("Chọn nút xuất phát trước", "err");
                  setPickingPath(selected);
                }}
                title="Tìm đường nối từ nút đang chọn tới nút khác"
              >
                <Icon name="route" size={18} />
              </button>
              <button className={darkBtn} onClick={() => setCamera(cameraTargetFor(visible.nodes, new Set(visible.nodes.map((n) => n.id)), 1.8))} title="Vừa khung">
                <Icon name="fit_screen" size={18} />
              </button>
              <button className={darkBtn + (panel === "filters" ? " " + darkBtnOn : "")} onClick={() => setPanel((p) => (p === "filters" ? null : "filters"))} title="Bộ lọc">
                <Icon name="filter_list" size={18} />
              </button>
              <button className={darkBtn + (panel === "display" ? " " + darkBtnOn : "")} onClick={() => setPanel((p) => (p === "display" ? null : "display"))} title="Hiển thị">
                <Icon name="tune" size={18} />
              </button>
            </div>
            {pickingPath && <div className="rounded-lg bg-[#e0a72e]/15 px-2.5 py-1.5 text-xs text-[#f3cf7a]">Chọn nút đích để tìm đường nối…</div>}
            {panel === "filters" && (
              <div className="flex max-h-[45vh] flex-col gap-3 overflow-auto border-t border-white/10 pt-2">
                <FilterList title="Loại trang" items={counts.types} hidden={hiddenTypes} color={typeColor} onToggle={(t) => setHiddenTypes((s) => toggle(s, t))} onOnly={(t) => setHiddenTypes(new Set(counts.types.map(([k]) => k).filter((k) => k !== t)))} onAll={() => setHiddenTypes(new Set())} />
                <FilterList title="Quan hệ" items={counts.rels} hidden={hiddenRels} color={edgeColor} line onToggle={(t) => setHiddenRels((s) => toggle(s, t))} onOnly={(t) => setHiddenRels(new Set(counts.rels.map(([k]) => k).filter((k) => k !== t)))} onAll={() => setHiddenRels(new Set())} />
              </div>
            )}
            {panel === "display" && <DisplayPanel value={display} onChange={setDisplay} />}
          </div>

          <div className={glass + " absolute bottom-3 left-3 z-10 flex flex-col gap-1.5 px-3 py-2.5 text-[11px]"}>
            <div className="flex max-w-130 flex-wrap gap-x-3 gap-y-1">
              {colorMode === "stellar"
                ? STELLAR_LEGEND.map((s) => (
                    <span key={s.color} className="flex items-center gap-1.5">
                      <i className="size-2.5 rounded-full" style={{ background: s.color }} />
                      {s.hint}
                    </span>
                  ))
                : counts.types.map(([t]) => (
                    <span key={t} className="flex items-center gap-1.5">
                      <i className="size-2.5 rounded-full" style={{ background: typeColor(t) }} />
                      {t}
                    </span>
                  ))}
            </div>
            <div className="text-[#8e918f]">
              {visible.nodes.length} nút · {visible.edges.length} cạnh · kéo để xoay, cuộn để phóng
            </div>
          </div>

          {selected && !pickingPath && <PageDrawer caseId={caseId} slug={selected} onClose={() => setSelected(null)} onGo={setSelected} />}
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------- panels

function NodeSearch({ nodes, onPick }: { nodes: GNode[]; onPick: (id: string) => void }) {
  const [q, setQ] = useState("");
  const [open, setOpen] = useState(false);
  const f = q.trim().toLowerCase();
  const list = nodes
    .filter((n) => !f || n.name.toLowerCase().includes(f) || n.id.includes(f))
    .sort((a, b) => b.degree - a.degree)
    .slice(0, 30);
  return (
    <div className="relative">
      <div className="flex h-9 items-center gap-2 rounded-lg bg-white/8 px-2.5 focus-within:bg-white/12">
        <Icon name="search" size={18} className="text-[#8e918f]" />
        <input
          className="min-w-0 flex-1 bg-transparent text-[13px] text-white outline-none placeholder:text-[#8e918f]"
          placeholder="Tìm trang…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onFocus={() => setOpen(true)}
          onBlur={() => setTimeout(() => setOpen(false), 150)}
        />
      </div>
      {open && (
        <div className="absolute top-10 right-0 left-0 z-20 max-h-80 overflow-auto rounded-lg border border-white/10 bg-[#1b2029] py-1 shadow-2xl">
          {!list.length && <div className="px-3 py-2 text-xs text-[#8e918f]">Không có trang</div>}
          {list.map((n) => (
            <button
              key={n.id}
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-white/8"
              onMouseDown={(ev) => ev.preventDefault()}
              onClick={() => {
                setOpen(false);
                setQ("");
                onPick(n.id);
              }}
            >
              <i className="size-2.5 flex-none rounded-full" style={{ background: typeColor(n.type) }} />
              <span className="min-w-0 flex-1 truncate">{n.name}</span>
              <span className="text-[11px] text-[#8e918f]">{n.type}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function PageDrawer({ caseId, slug, onClose, onGo }: { caseId: string; slug: string; onClose: () => void; onGo: (slug: string) => void }) {
  const [p, setP] = useState<WikiPage | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    let alive = true;
    setP(null);
    setErr("");
    getWikiPage(caseId, slug).then(
      (x) => alive && setP(x),
      (x) => alive && setErr((x as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [caseId, slug]);

  const section = "border-t border-white/10 px-4 py-3";
  const h3 = "mb-2 text-[11px] font-medium tracking-wide text-[#8e918f] uppercase";
  const links = [...(p?.links_out ?? []).map((l) => ({ ...l, dir: "out" as const })), ...(p?.links_in ?? []).map((l) => ({ ...l, dir: "in" as const }))];
  return (
    <aside className={glass + " absolute top-3 right-3 bottom-3 z-10 flex w-90 max-w-[calc(100%-24px)] flex-col overflow-hidden"}>
      {err && <div className="p-4 text-sm text-[#f28b82]">{err}</div>}
      {!p && !err && (
        <div className="grid flex-1 place-items-center">
          <Spinner className="border-white/20 border-t-[#a8c7fa]" />
        </div>
      )}
      {p && (
        <div className="min-h-0 flex-1 overflow-auto">
          <div className="px-4 pt-4 pb-3">
            <div className="flex items-start gap-2">
              <i className="mt-1.5 size-3 flex-none rounded-full" style={{ background: typeColor(nodeType(p)) }} />
              <div className="min-w-0 flex-1">
                <h2 className="text-lg leading-6 wrap-break-word text-white">{p.title}</h2>
                <div className="mt-1 text-xs text-[#8e918f]">
                  {nodeType(p)} · {p.footnotes?.length ?? 0} chú thích
                </div>
              </div>
              <button className="grid size-8 place-items-center rounded-full text-[#c4c7c5] hover:bg-white/10" onClick={onClose} aria-label="Đóng">
                <Icon name="close" size={20} />
              </button>
            </div>
            {p.summary && <p className="mt-3 text-[13px] leading-5 text-[#c4c7c5]">{p.summary}</p>}
            <div className="mt-3 flex flex-wrap gap-1.5">
              <Link className={darkBtn + " border border-white/15"} to={`/wiki/${caseId}/${p.slug.split("/").map(encodeURIComponent).join("/")}`}>
                <Icon name="menu_book" size={18} /> Mở trang wiki
              </Link>
              {p.document_id && (
                <Link className={darkBtn + " border border-white/15"} to={`/documents/${p.document_id}`}>
                  <Icon name="description" size={18} /> Tài liệu gốc
                </Link>
              )}
            </div>
          </div>
          {!!Object.keys(p.attributes ?? {}).length && (
            <div className={section}>
              <h3 className={h3}>Thuộc tính</h3>
              <dl className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-x-3 gap-y-1.5 text-[13px]">
                {Object.entries(p.attributes ?? {}).map(([k, a]) => (
                  <div key={k} className="contents">
                    <dt className="truncate text-[#8e918f]" title={k}>
                      {k}
                    </dt>
                    <dd className={"wrap-break-word " + (a.conflict ? "text-[#f3cf7a]" : "")}>{a.conflict ? (a.history ?? []).map((h) => metaValue(h.value)).join(" ≠ ") : metaValue(a.value)}</dd>
                  </div>
                ))}
              </dl>
            </div>
          )}
          <div className={section}>
            <h3 className={h3}>Liên kết ({links.length})</h3>
            {links.map((l, i) => {
              const other = l.dir === "out" ? l.to : l.from;
              return (
                <button key={i} className="flex w-full items-baseline gap-1.5 rounded px-1 py-1 text-left text-[13px] hover:bg-white/5" onClick={() => onGo(other)}>
                  <span className="text-[#8e918f]">{l.dir === "out" ? "→" : "←"}</span>
                  {l.relation && (
                    <span className="font-mono text-[11px] whitespace-nowrap" style={{ color: edgeColor(l.relation) }}>
                      {l.relation}
                    </span>
                  )}
                  <span className="min-w-0 flex-1 truncate">{(l.dir === "out" ? l.to_title : l.from_title) || other}</span>
                </button>
              );
            })}
            {!links.length && <span className="text-xs text-[#8e918f]">—</span>}
          </div>
        </div>
      )}
    </aside>
  );
}

function FilterList({
  title,
  items,
  hidden,
  color,
  line,
  onToggle,
  onOnly,
  onAll,
}: {
  title: string;
  items: [string, number][];
  hidden: Set<string>;
  color: (t: string) => string;
  line?: boolean;
  onToggle: (t: string) => void;
  onOnly: (t: string) => void;
  onAll: () => void;
}) {
  if (!items.length) return null;
  return (
    <div>
      <div className="mb-1 flex items-center px-1 text-[11px] font-medium tracking-wide text-[#8e918f] uppercase">
        <span className="flex-1">{title}</span>
        {hidden.size > 0 && (
          <button className="normal-case text-[#a8c7fa] hover:underline" onClick={onAll}>
            Hiện tất cả
          </button>
        )}
      </div>
      {items.map(([t, n]) => {
        const off = hidden.has(t);
        return (
          <div key={t} className="group flex h-7 items-center gap-2 rounded-md px-1 hover:bg-white/5">
            <button className="flex min-w-0 flex-1 items-center gap-2 text-left text-[13px]" onClick={() => onToggle(t)}>
              <Icon name={off ? "check_box_outline_blank" : "check_box"} size={18} className={off ? "text-[#8e918f]" : "text-[#a8c7fa]"} />
              {line ? <i className="h-0.5 w-3 flex-none rounded" style={{ background: color(t) }} /> : <i className="size-2.5 flex-none rounded-full" style={{ background: color(t) }} />}
              <span className={"min-w-0 flex-1 truncate " + (off ? "text-[#8e918f]" : "")}>{t}</span>
            </button>
            <button className="hidden text-[11px] text-[#a8c7fa] group-hover:inline" onClick={() => onOnly(t)}>
              chỉ
            </button>
            <span className="text-[11px] text-[#8e918f] tabular-nums">{n}</span>
          </div>
        );
      })}
    </div>
  );
}

function DisplayPanel({ value, onChange }: { value: DisplaySettings; onChange: (d: DisplaySettings) => void }) {
  const rows: [keyof DisplaySettings, string, string][] = [
    ["edgeBrightness", "Độ sáng cạnh", "tăng khi cạnh quá mờ"],
    ["nodeGlow", "Quầng sáng nút", "0 = màu phẳng"],
    ["bloom", "Bloom", "độ loá toàn cảnh"],
  ];
  return (
    <div className="flex flex-col gap-3 border-t border-white/10 px-1 pt-2">
      {rows.map(([k, label, hint]) => (
        <label key={k} className="block">
          <div className="mb-1 flex items-center justify-between text-[12px]">
            <span>{label}</span>
            <span className="font-mono text-[#a8c7fa] tabular-nums">{value[k].toFixed(2)}×</span>
          </div>
          <input
            type="range"
            min={DISPLAY_LIMITS[k].min}
            max={DISPLAY_LIMITS[k].max}
            step={0.05}
            value={value[k]}
            onChange={(e) => onChange({ ...value, [k]: parseFloat(e.target.value) })}
            className="w-full cursor-pointer accent-[#a8c7fa]"
          />
          <p className="text-[10px] text-[#8e918f]">{hint}</p>
        </label>
      ))}
      <button className="self-end text-xs text-[#a8c7fa] hover:underline" onClick={() => onChange(DEFAULT_DISPLAY)}>
        Mặc định
      </button>
    </div>
  );
}

