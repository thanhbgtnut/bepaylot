import type { DocumentPage } from "../api/types";
import { Badge, Icon, Spinner } from "./ui";

// Trạng thái từng trang cho người nghiệp vụ (U111): chưa xử lý (không được
// chọn), đang xử lý, lỗi, hoặc đã xử lý với từng bước đã có: dòng chữ, nội
// dung, phân loại (chỉ khi thư viện có loại giấy tờ).

const BUSY = new Set(["pending", "rendering", "rendered", "ocr"]);

// "13–14, 20" from [13, 14, 20].
export function spans(ps: number[]) {
  const out: [number, number][] = [];
  for (const p of ps) {
    const last = out.at(-1);
    if (last && last[1] === p - 1) last[1] = p;
    else out.push([p, p]);
  }
  return out.map(([a, b]) => (a === b ? `${a}` : `${a}–${b}`)).join(", ");
}

export const pagesWith = (pages: DocumentPage[], status: string) => pages.filter((p) => p.status === status).map((p) => p.page_no);

// One line: "11/14 trang đã xử lý · lỗi tr. 12 · chưa xử lý tr. 13–14"; empty
// when every page is done.
export function pageSummary(pages: DocumentPage[]) {
  const done = pagesWith(pages, "done").length;
  if (done === pages.length) return "";
  const failed = pagesWith(pages, "failed");
  const skipped = pagesWith(pages, "skipped");
  const busy = pages.filter((p) => BUSY.has(p.status)).length;
  return [
    `${done}/${pages.length} trang đã xử lý`,
    busy && `${busy} trang đang xử lý`,
    failed.length && `lỗi tr. ${spans(failed)}`,
    skipped.length && `chưa xử lý tr. ${spans(skipped)}`,
  ]
    .filter(Boolean)
    .join(" · ");
}

// The run of skipped pages starting at n: [n, last skipped page after it].
export function skippedRun(pages: DocumentPage[], n: number): [number, number] {
  let end = n;
  while (pages.find((p) => p.page_no === end + 1)?.status === "skipped") end++;
  return [n, end];
}

export function PageSteps({ page, classify, compact }: { page: DocumentPage; classify: boolean; compact?: boolean }) {
  if (page.status === "skipped") return <Badge title="Trang chưa được chọn để đọc và phân loại">Chưa xử lý</Badge>;
  if (page.status === "failed")
    return (
      <Badge tone="err" title={page.error}>
        <Icon name="error" size={14} />
        Lỗi đọc trang
      </Badge>
    );
  if (BUSY.has(page.status))
    return (
      <Badge tone="info">
        <Spinner className="size-2.5 border-[1.5px]" />
        Đang xử lý
      </Badge>
    );
  const steps: [string, boolean][] = [
    ["Dòng chữ", !!page.text_source || page.is_blank],
    ["Nội dung", !!page.markdown?.trim() || page.is_blank],
    ...(classify ? ([["Phân loại", !!page.doc_type]] as [string, boolean][]) : []),
  ];
  if (compact && steps.every(([, ok]) => ok))
    return (
      <Badge tone="ok" title={steps.map(([l]) => l).join(", ") + ": xong"}>
        <Icon name="check" size={14} />
        Đã xử lý
      </Badge>
    );
  return (
    <>
      {steps.map(([l, ok]) => (
        <Badge key={l} tone={ok ? "ok" : ""} title={`${l}: ${ok ? "xong" : "không có"}`}>
          <Icon name={ok ? "check" : "remove"} size={14} />
          {l}
        </Badge>
      ))}
    </>
  );
}
