import { useMemo, useState, type ReactNode } from "react";

import { useToast } from "../../components/toast";
import { FileIcon, Icon, Modal } from "../../components/ui";

// One file of the picker: its page count is known (split has counted it).
export interface PickFile {
  id: string;
  name: string;
  mime?: string;
  pages: number;
  note?: ReactNode;
  /** Không tick sẵn (file đã xử lý trước đó). Bỏ qua khi chỉ có một file. */
  off?: boolean;
}

export interface Picked {
  id: string;
  pages: number[];
}

const pagesLabel = (a: number, b: number) => (a === b ? `tr. ${a}` : `tr. ${a}–${b}`);

// Chọn file (mặc định chọn hết) và khoảng trang riêng của từng file: từ
// trang … đến trang …, mặc định toàn bộ (U111). Thanh áp dụng hàng loạt điền
// một khoảng cho mọi file đang tick; sau đó vẫn sửa riêng từng file. Khoảng
// không khớp số trang của file thì báo ở dòng và chặn nút chính cho tới khi
// sửa. Chỉ các trang được chọn mới đi qua luồng xử lý. Dùng cho bước 2 của
// tải lên và cho Đọc lại.
export function PagePicker({
  title,
  icon = "upload_file",
  head,
  files,
  range,
  later,
  ok = "Đọc & phân loại",
  onClose,
  onSubmit,
}: {
  title: string;
  icon?: string;
  head?: ReactNode;
  files: PickFile[];
  /** Khoảng điền sẵn khi chỉ có một file. */
  range?: [number, number];
  /** Nút "Để sau" thay cho "Huỷ": file không chọn giữ trạng thái Chưa xử lý. */
  later?: boolean;
  ok?: string;
  onClose: () => void;
  onSubmit: (picked: Picked[]) => Promise<void>;
}) {
  const { fail } = useToast();
  const one = files.length === 1;
  const [sel, setSel] = useState(() => new Set(files.map((_, i) => i).filter((i) => one || !files[i].off)));
  const [ranges, setRanges] = useState<[number, number][]>(() => files.map((f) => (one && range ? range : [1, f.pages])));
  const [q, setQ] = useState("");
  const [last, setLast] = useState<number | null>(null);
  const [bulk, setBulk] = useState<[number, number]>([1, 1]);
  const [busy, setBusy] = useState(false);

  // Sai: khoảng không hợp lệ. Thiếu: file không có trang bắt đầu. Vượt: "đến
  // trang" lớn hơn trang cuối.
  const bad = (i: number) => ranges[i][0] < 1 || ranges[i][0] > ranges[i][1];
  const missing = (i: number) => !bad(i) && ranges[i][0] > files[i].pages;
  const over = (i: number) => !bad(i) && !missing(i) && ranges[i][1] > files[i].pages;
  const count = (i: number) => (bad(i) || missing(i) ? 0 : Math.min(ranges[i][1], files[i].pages) - ranges[i][0] + 1);
  const visible = useMemo(() => files.map((_, i) => i).filter((i) => !q || files[i].name.toLowerCase().includes(q.toLowerCase())), [files, q]);
  const picked = [...sel];
  const total = picked.reduce((a, i) => a + count(i), 0);
  const wrong = picked.filter(bad);
  const miss = picked.filter(missing);
  const ov = picked.filter(over);
  const nVis = visible.filter((i) => sel.has(i)).length;

  const toggle = (is: number[], on: boolean) =>
    setSel((s) => {
      const n = new Set(s);
      for (const i of is) if (on) n.add(i);
      else n.delete(i);
      return n;
    });
  const setRange = (is: number[], f: (i: number) => [number, number]) => setRanges((rs) => rs.map((r, i) => (is.includes(i) ? f(i) : r)));
  const bulkOk = bulk[0] >= 1 && bulk[1] >= bulk[0];

  const submit = async () => {
    setBusy(true);
    try {
      await onSubmit(picked.map((i) => ({ id: files[i].id, pages: Array.from({ length: count(i) }, (_, k) => ranges[i][0] + k) })));
    } catch (e) {
      fail(e);
      setBusy(false);
    }
  };

  const num = (i: number, k: 0 | 1) => (
    <input
      type="number"
      min={1}
      max={files[i].pages}
      aria-label={k ? "Đến trang" : "Từ trang"}
      disabled={!sel.has(i)}
      value={ranges[i][k] || ""}
      onChange={(e) => setRange([i], () => (k ? [ranges[i][0], +e.target.value || 0] : [+e.target.value || 0, ranges[i][1]]))}
      className={
        "input h-8 w-14 px-1 text-center disabled:opacity-40 " +
        (sel.has(i) && bad(i) ? "border-err " : sel.has(i) && (k ? over(i) : missing(i)) ? "border-warn " : "")
      }
    />
  );
  const row = (i: number) => {
    const f = files[i];
    const on = sel.has(i);
    const note = !on
      ? ""
      : missing(i)
        ? `Không có trang ${ranges[i][0]} — file chỉ có ${f.pages} trang`
        : over(i)
          ? `Không có trang ${f.pages + 1}–${ranges[i][1]} — file chỉ có ${f.pages} trang`
          : "";
    return (
      <div key={f.id} className={"flex min-h-11 items-center gap-3 px-3 py-1 text-sm " + (one ? "" : "border-t border-line ") + (on ? "text-fg " : "text-subtle ") + (note ? "bg-warn-soft/50" : "")}>
        {!one && (
          <input
            type="checkbox"
            className="size-4 accent-accent"
            aria-label={`Chọn ${f.name}`}
            checked={on}
            onChange={() => {}}
            onClick={(e) => {
              // Shift + tick: cả dải file đang hiện từ lần tick trước tới file này.
              const a = last !== null ? visible.indexOf(last) : -1;
              const b = visible.indexOf(i);
              toggle(e.shiftKey && a >= 0 ? visible.slice(Math.min(a, b), Math.max(a, b) + 1) : [i], e.currentTarget.checked);
              setLast(i);
            }}
          />
        )}
        <FileIcon mime={f.mime} name={f.name} size={18} />
        <span className="min-w-0 flex-1">
          <span className="block truncate">{f.name}</span>
          {f.note && <span className="block text-xs text-muted">{f.note}</span>}
          {note && <span className="block text-xs text-warn">{note}</span>}
        </span>
        <span className="flex items-center gap-1.5 text-xs whitespace-nowrap text-muted">
          Từ trang {num(i, 0)} đến {num(i, 1)} <span className="w-9">/ {f.pages}</span>
        </span>
      </div>
    );
  };

  const label = wrong.length
    ? `Sửa khoảng trang của ${wrong.length} file`
    : miss.length || ov.length
      ? one
        ? "Khoảng trang không khớp"
        : `${miss.length + ov.length} file không khớp số trang`
      : one
        ? `${ok} ${total} trang`
        : `${ok} ${sel.size} file · ${total} trang`;

  return (
    <Modal
      title={title}
      icon={icon}
      onClose={onClose}
      width={680}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            {later ? "Để sau" : "Huỷ"}
          </button>
          <button className="btn btn-primary" disabled={busy || !!(wrong.length || miss.length || ov.length) || !total} onClick={submit}>
            {busy ? "Đang gửi…" : label}
          </button>
        </>
      }
    >
      {head}
      {one ? (
        <>
          <div className="rounded-xl border border-line">{row(0)}</div>
          <p className="-mt-2 text-xs text-subtle">Trang ngoài khoảng giữ nguyên kết quả cũ.</p>
        </>
      ) : (
        <>
          <input className="input" placeholder="Tìm file theo tên, rồi tick đầu bảng để chọn các file đang hiện" value={q} onChange={(e) => setQ(e.target.value)} />
          <div className="overflow-hidden rounded-xl border border-line">
            <div className="flex h-10 items-center gap-3 bg-surface-2 px-3 text-xs font-medium">
              <input
                type="checkbox"
                className="size-4 accent-accent"
                aria-label="Chọn các file đang hiện"
                checked={visible.length > 0 && nVis === visible.length}
                ref={(el) => {
                  if (el) el.indeterminate = nVis > 0 && nVis < visible.length;
                }}
                onChange={(e) => toggle(visible, e.target.checked)}
              />
              <span className="flex-1">
                Đã chọn {sel.size}/{files.length} file · {total} trang{q ? ` · đang hiện ${visible.length} file` : ""}
              </span>
              <span>Trang cần xử lý</span>
            </div>
            {sel.size > 0 && (
              <div className="flex flex-wrap items-center gap-2 border-t border-line bg-accent-soft/40 px-3 py-2 text-xs text-fg">
                <span>Từ trang</span>
                <input type="number" min={1} aria-label="Từ trang (hàng loạt)" className="input h-8 w-14 px-1 text-center" value={bulk[0] || ""} onChange={(e) => setBulk([+e.target.value || 0, bulk[1]])} />
                <span>đến</span>
                <input type="number" min={1} aria-label="Đến trang (hàng loạt)" className="input h-8 w-14 px-1 text-center" value={bulk[1] || ""} onChange={(e) => setBulk([bulk[0], +e.target.value || 0])} />
                <button className="btn btn-tonal btn-sm" disabled={!bulkOk} onClick={() => setRange(picked, () => bulk)}>
                  Áp dụng {bulkOk ? pagesLabel(bulk[0], bulk[1]) + " " : ""}cho {sel.size} file đã chọn
                </button>
                <span className="flex-1" />
                <button className="btn btn-text btn-sm" onClick={() => setRange(picked, (i) => [1, files[i].pages])}>
                  Đặt lại toàn bộ trang
                </button>
              </div>
            )}
            <div className="max-h-80 overflow-auto">
              {visible.map(row)}
              {!visible.length && <div className="border-t border-line px-3 py-4 text-center text-xs text-subtle">Không có file nào khớp</div>}
            </div>
          </div>
          <p className="-mt-2 text-xs text-subtle">
            Mặc định xử lý toàn bộ trang của mỗi file. Giữ Shift khi tick để chọn liền một dải file. File không chọn vẫn được lưu ở trạng thái “Chưa xử lý”.
          </p>
        </>
      )}
      {(miss.length > 0 || ov.length > 0) && (
        <div className="flex gap-2 rounded-xl bg-warn-soft px-4 py-3 text-sm text-fg">
          <Icon name="warning" size={20} className="text-warn" />
          <div className="min-w-0 flex-1">
            <div className="font-medium">{one ? "Khoảng trang không khớp số trang của file" : `${miss.length + ov.length} file không khớp số trang`}</div>
            <div className="mt-0.5 text-xs text-muted">
              {[ov.length && `${one ? "Đến trang" : `${ov.length} file có “đến trang”`} lớn hơn trang cuối`, miss.length && `${one ? "Không có" : `${miss.length} file không có`} trang bắt đầu`]
                .filter(Boolean)
                .join(" · ")}
              . Sửa ở dòng tô vàng hoặc:
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              {ov.length > 0 && (
                <button className="btn btn-sm bg-surface" onClick={() => setRange(ov, (i) => [ranges[i][0], files[i].pages])}>
                  Lấy tới trang cuối{one ? "" : ` cho ${ov.length} file`}
                </button>
              )}
              {miss.length > 0 && !one && (
                <button className="btn btn-sm bg-surface" onClick={() => toggle(miss, false)}>
                  Bỏ chọn {miss.length} file không có trang bắt đầu
                </button>
              )}
            </div>
          </div>
        </div>
      )}
      <p className="text-xs text-subtle">Bảng Excel chỉ lấy thông tin từ các trang đã phân loại.</p>
    </Modal>
  );
}
