import { createContext, useCallback, useContext, useState, type ReactNode } from "react";

type Kind = "info" | "err";
interface Toast {
  id: number;
  msg: string;
  kind: Kind;
}

interface ToastApi {
  toast: (msg: string, kind?: Kind) => void;
  fail: (e: unknown) => void;
}

const Ctx = createContext<ToastApi>({ toast: () => {}, fail: () => {} });
export const useToast = () => useContext(Ctx);

let seq = 0;

// M3 snackbars, bottom-left like Drive.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);
  const dismiss = (id: number) => setItems((xs) => xs.filter((x) => x.id !== id));
  const toast = useCallback((msg: string, kind: Kind = "info") => {
    const id = ++seq;
    setItems((xs) => [...xs.slice(-2), { id, msg, kind }]);
    setTimeout(() => dismiss(id), kind === "err" ? 7000 : 4000);
  }, []);
  const fail = useCallback(
    (e: unknown) => {
      if ((e as Error)?.name === "AbortError") return;
      console.error(e);
      toast((e as Error)?.message ?? String(e), "err");
    },
    [toast],
  );
  return (
    <Ctx.Provider value={{ toast, fail }}>
      {children}
      <div className="fixed bottom-6 left-6 z-1000 flex flex-col gap-2">
        {items.map((t) => (
          <div key={t.id} className="flex min-h-12 max-w-130 items-center gap-3 rounded-lg bg-[#303030] py-2.5 pr-2 pl-4 text-sm text-[#f2f2f2] shadow-3">
            {t.kind === "err" && <span className="material-symbols-rounded text-[20px] text-[#f28b82]">error</span>}
            <span className="flex-1">{t.msg}</span>
            <button className="btn-icon btn-sm text-[#c4c7c5] hover:bg-white/10" onClick={() => dismiss(t.id)} aria-label="Đóng">
              <span className="material-symbols-rounded text-[20px]">close</span>
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}
