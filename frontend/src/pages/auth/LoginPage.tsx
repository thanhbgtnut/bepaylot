// Login / register page, laid out like WeKnora's: a showcase of the product on
// the left over a field of drifting knowledge nodes, and a card on the right
// that switches between signing in and creating an account, with an OIDC
// button when the server enables it. Styled in the app's Material 3 look.
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { login, oidcStartURL, readOIDCHash, register } from "../../api/auth";
import { settings } from "../../api/client";
import { useAuth } from "../../components/AuthContext";
import { Logo } from "../../components/Layout";
import { Icon, Modal, Spinner } from "../../components/ui";

type Mode = "login" | "register";

const TAGS = ["OCR + text layer", "LLM Wiki", "Search vectorless", "Agent theo hồ sơ"];

const SLIDES: { icon: string; title: string; text: string }[] = [
  { icon: "document_scanner", title: "Parse mọi trang hồ sơ", text: "TurboOCR lấy layout, VLM đọc từng vùng, text layer PDF/A bổ sung — mỗi dòng giữ trang và toạ độ." },
  { icon: "menu_book", title: "Wiki riêng cho từng hồ sơ", text: "Entity, quan hệ và chú thích về dòng gốc được dựng sẵn khi index, đọc như DeepWiki." },
  { icon: "forum", title: "Hỏi agent, nhận trích dẫn", text: "Agent chỉ tìm trong đúng một case, bóc tách trường thông tin và kiểm rule kèm nguồn." },
];

const FEATURES = ["Parse đa định dạng, tra ngược tới trang và dòng", "Tìm kiếm không cần embedding, cứng trong một case", "Hỏi đáp có trích dẫn tới trang gốc"];

// Background nodes: position (%), icon, drift offset and delay.
const NODES: [number, number, string, number, number, number][] = [
  [18, 14, "description", 14, -18, 0],
  [34, 26, "folder_open", -12, 14, 2],
  [54, 18, "hub", 10, 16, 4],
  [84, 11, "database", -16, -10, 1],
  [7, 36, "search", 12, 12, 3],
  [25, 47, "account_tree", -10, -14, 5],
  [64, 47, "menu_book", 14, -12, 2.5],
  [90, 38, "forum", -12, 16, 1.5],
  [20, 62, "task_alt", 16, 10, 4.5],
  [42, 72, "fact_check", -14, -12, 0.5],
  [75, 80, "star", 12, -16, 3.5],
  [60, 90, "group", -10, 12, 6],
];
const LINES: [number, number][] = [
  [0, 1], [1, 2], [2, 3], [4, 5], [5, 6], [6, 7], [0, 8], [2, 5], [6, 11], [9, 10], [1, 5], [8, 9],
];

export function LoginPage() {
  const { user, config, configError, signIn, reload } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  const from = (loc.state as { from?: string } | null)?.from || "/documents";

  const [mode, setMode] = useState<Mode>("login");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [showPw, setShowPw] = useState(false);
  const [busy, setBusy] = useState(false);
  const [oidcBusy, setOidcBusy] = useState(false);
  const [error, setError] = useState("");
  const [serverOpen, setServerOpen] = useState(false);

  const regOpen = !!config?.registration_enabled;
  const minLen = config?.password_min_length || 8;
  const oidc = config?.oidc?.enabled ? config.oidc : null;

  // Result of the OIDC redirect flow arrives in the URL fragment.
  useEffect(() => {
    const r = readOIDCHash(location.hash);
    if (!r) return;
    history.replaceState(null, "", location.pathname + location.search);
    if (r.tokens) signIn(r.tokens);
    else if (r.error) setError(r.error);
  }, [signIn]);

  useEffect(() => {
    if (user) nav(from, { replace: true });
  }, [user, from, nav]);

  const switchMode = (m: Mode) => {
    setMode(m);
    setError("");
    setPassword("");
    setConfirm("");
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (mode === "register") {
      if (password.length < minLen) return setError(`Mật khẩu cần ít nhất ${minLen} ký tự`);
      if (password !== confirm) return setError("Mật khẩu nhập lại không khớp");
    }
    setBusy(true);
    try {
      signIn(mode === "login" ? await login(email.trim(), password) : await register({ email: email.trim(), name: name.trim(), password }));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const startOIDC = () => {
    setOidcBusy(true);
    location.href = oidcStartURL();
  };

  return (
    <div className="relative flex min-h-full overflow-hidden bg-bg">
      <Background />

      <header className="absolute inset-x-0 top-0 z-10 flex h-16 items-center gap-2.5 px-4 md:px-8">
        <Logo />
        <span className="text-[22px] text-muted">BePaylot</span>
        <div className="flex-1" />
        <button className="btn btn-text btn-sm text-muted" onClick={() => setServerOpen(true)} title="Địa chỉ máy chủ API">
          <Icon name="dns" size={18} />
          <span className="max-sm:hidden">{settings.base || location.host}</span>
        </button>
      </header>

      {/* Showcase */}
      <section className="relative z-1 hidden flex-1 items-center justify-center px-12 pt-16 lg:flex">
        <div className="relative max-w-140 rounded-[32px] bg-bg/60 p-2 backdrop-blur-[2px]">
          <p className="text-4xl leading-11 font-normal text-fg">
            Hồ sơ nghiệp vụ,
            <br />
            <span className="text-accent">đọc hiểu và hỏi đáp</span> trong một nơi
          </p>
          <p className="mt-4 text-base leading-6 text-muted">
            BePaylot parse tài liệu theo từng trang, dựng wiki cho mỗi hồ sơ và để agent tìm đúng thông tin trong đúng hồ sơ — không nhầm sang hồ sơ khác.
          </p>
          <div className="mt-6 flex flex-wrap gap-2">
            {TAGS.map((t) => (
              <span key={t} className="inline-flex h-8 items-center rounded-lg bg-accent-soft px-3 text-[13px] font-medium text-on-accent-soft">
                {t}
              </span>
            ))}
          </div>
          <Showcase />
        </div>
      </section>

      {/* Form */}
      <section className="relative z-1 flex w-full items-center justify-center px-4 pt-20 pb-8 lg:w-[520px] lg:flex-none lg:pr-12">
        <div key={mode} className="w-full max-w-105 animate-fade-up rounded-[28px] bg-surface p-8 shadow-2 max-sm:px-5">
          <h1 className="text-[28px] leading-9 font-normal text-fg">{mode === "login" ? "Đăng nhập" : "Tạo tài khoản"}</h1>
          <p className="mt-1 text-sm text-muted">
            {mode === "login" ? "Chào mừng trở lại BePaylot" : "Mỗi tài khoản có knowledge base, hồ sơ và phiên hỏi đáp riêng"}
          </p>

          {configError && (
            <Banner tone="err">
              Không kết nối được máy chủ: {configError}{" "}
              <button className="font-medium underline" onClick={() => setServerOpen(true)}>
                Đổi địa chỉ
              </button>
            </Banner>
          )}
          {config?.auth_bypass && (
            <Banner tone="info">
              Máy chủ đang chạy <code>auth_bypass</code> (chỉ dành cho dev) — không cần đăng nhập.{" "}
              <button className="font-medium underline" onClick={reload}>
                Vào ứng dụng
              </button>
            </Banner>
          )}
          {error && <Banner tone="err">{error}</Banner>}

          <form className="mt-6 flex flex-col gap-4" onSubmit={submit}>
            {mode === "register" && (
              <Field label="Họ tên">
                <input className="input h-12" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Nguyễn Văn An" disabled={busy} />
              </Field>
            )}
            <Field label="Email">
              <input
                className="input h-12"
                type="email"
                required
                autoFocus
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="ban@congty.vn"
                disabled={busy}
              />
            </Field>
            <div className="field">
              <label htmlFor="login-password">Mật khẩu</label>
              <div className="relative flex">
                <input
                  id="login-password"
                  className="input h-12 flex-1 pr-12"
                  type={showPw ? "text" : "password"}
                  required
                  autoComplete={mode === "login" ? "current-password" : "new-password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={mode === "login" ? "Nhập mật khẩu" : `Ít nhất ${minLen} ký tự`}
                  disabled={busy}
                />
                <button type="button" className="btn-icon btn-sm absolute top-2 right-2" onClick={() => setShowPw((v) => !v)} aria-label={showPw ? "Ẩn mật khẩu" : "Hiện mật khẩu"}>
                  <Icon name={showPw ? "visibility_off" : "visibility"} size={20} />
                </button>
              </div>
            </div>
            {mode === "register" && (
              <Field label="Nhập lại mật khẩu">
                <input
                  className="input h-12"
                  type={showPw ? "text" : "password"}
                  required
                  autoComplete="new-password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  disabled={busy}
                />
              </Field>
            )}

            <button type="submit" className="btn btn-primary mt-2 h-12 w-full text-[15px]" disabled={busy || !config}>
              {busy && <Spinner className="size-4 border-on-accent/30 border-t-on-accent" />}
              {mode === "login" ? (busy ? "Đang đăng nhập…" : "Đăng nhập") : busy ? "Đang tạo tài khoản…" : "Tạo tài khoản"}
            </button>
          </form>

          {mode === "login" && regOpen && (
            <>
              <Divider>Lần đầu dùng BePaylot?</Divider>
              <button className="btn h-12 w-full" onClick={() => switchMode("register")} disabled={busy}>
                <Icon name="person_add" size={20} />
                Tạo tài khoản
              </button>
            </>
          )}
          {mode === "register" && (
            <p className="mt-5 text-center text-sm text-muted">
              Đã có tài khoản?{" "}
              <button className="font-medium text-accent hover:underline" onClick={() => switchMode("login")}>
                Đăng nhập
              </button>
            </p>
          )}

          {oidc && (
            <>
              <Divider>hoặc tiếp tục với</Divider>
              <button className="btn btn-tonal h-12 w-full" onClick={startOIDC} disabled={busy || oidcBusy}>
                {oidcBusy ? <Spinner className="size-4" /> : <Icon name="shield_person" size={20} />}
                {oidcBusy ? "Đang chuyển tới nhà cung cấp…" : `Đăng nhập bằng ${oidc.display_name || "SSO"}`}
              </button>
            </>
          )}

          {mode === "login" && (
            <ul className="mt-7 flex flex-col gap-2 border-t border-line pt-5">
              {FEATURES.map((f) => (
                <li key={f} className="flex items-center gap-2 text-[13px] text-muted">
                  <Icon name="check_circle" size={18} fill className="text-ok" />
                  {f}
                </li>
              ))}
            </ul>
          )}
          {!regOpen && mode === "login" && config && !oidc && (
            <p className="mt-4 text-xs text-subtle">Đăng ký đang tắt — liên hệ quản trị viên để được cấp tài khoản.</p>
          )}
        </div>
      </section>

      {serverOpen && <ServerDialog onClose={() => setServerOpen(false)} onSaved={reload} />}
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
    </label>
  );
}

function Divider({ children }: { children: ReactNode }) {
  return (
    <div className="my-5 flex items-center gap-3 text-xs text-subtle">
      <span className="h-px flex-1 bg-line" />
      {children}
      <span className="h-px flex-1 bg-line" />
    </div>
  );
}

function Banner({ tone, children }: { tone: "err" | "info"; children: ReactNode }) {
  return (
    <div className={"mt-4 flex gap-2 rounded-xl px-3 py-2.5 text-[13px] " + (tone === "err" ? "bg-err-soft text-err" : "bg-accent-soft text-on-accent-soft")}>
      <Icon name={tone === "err" ? "error" : "info"} size={18} className="mt-px" />
      <div className="min-w-0 flex-1 break-words">{children}</div>
    </div>
  );
}

// A small auto-advancing carousel of what the product does (WeKnora shows
// screenshots here).
function Showcase() {
  const [i, setI] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setI((x) => (x + 1) % SLIDES.length), 4500);
    return () => clearInterval(t);
  }, []);
  const s = SLIDES[i];
  return (
    <div className="mt-10">
      <div key={i} className="flex animate-fade-up gap-4 rounded-3xl bg-surface/80 p-6 shadow-1 backdrop-blur">
        <div className="grid size-12 flex-none place-items-center rounded-2xl bg-accent-container text-accent">
          <Icon name={s.icon} size={26} />
        </div>
        <div>
          <div className="text-base font-medium text-fg">{s.title}</div>
          <div className="mt-1 text-sm leading-5 text-muted">{s.text}</div>
        </div>
      </div>
      <div className="mt-4 flex gap-2">
        {SLIDES.map((_, k) => (
          <button
            key={k}
            onClick={() => setI(k)}
            aria-label={`Trang ${k + 1}`}
            className={"h-1.5 rounded-full transition-all " + (k === i ? "w-6 bg-accent" : "w-1.5 bg-outline/50 hover:bg-outline")}
          />
        ))}
      </div>
    </div>
  );
}

function Background() {
  return (
    <div className="pointer-events-none absolute inset-0" aria-hidden>
      <div className="absolute -top-40 -left-40 size-[520px] rounded-full bg-accent-container/50 blur-3xl" />
      <div className="absolute -right-32 -bottom-48 size-[480px] rounded-full bg-accent-soft/40 blur-3xl" />
      <svg className="absolute inset-0 size-full" viewBox="0 0 100 100" preserveAspectRatio="none">
        {LINES.map(([a, b], k) => (
          <line
            key={k}
            className="login-line"
            x1={NODES[a][0]}
            y1={NODES[a][1]}
            x2={NODES[b][0]}
            y2={NODES[b][1]}
            stroke="var(--accent)"
            strokeOpacity={0.18}
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
          />
        ))}
      </svg>
      {NODES.map(([x, y, icon, dx, dy, delay], k) => (
        <div
          key={k}
          className="absolute grid size-11 -translate-1/2 animate-drift place-items-center rounded-2xl bg-surface/60 text-accent/45 shadow-1 max-lg:hidden"
          style={{ left: `${x}%`, top: `${y}%`, animationDelay: `-${delay}s`, ["--dx" as string]: `${dx}px`, ["--dy" as string]: `${dy}px` }}
        >
          <Icon name={icon} size={20} />
        </div>
      ))}
    </div>
  );
}

function ServerDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [base, setBase] = useState(settings.base);
  return (
    <Modal
      title="Máy chủ API"
      icon="dns"
      onClose={onClose}
      width={480}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button
            className="btn btn-primary"
            onClick={() => {
              settings.base = base;
              onClose();
              onSaved();
            }}
          >
            Lưu
          </button>
        </>
      }
    >
      <label className="field">
        <span>API base URL</span>
        <input className="input" value={base} onChange={(e) => setBase(e.target.value)} placeholder="để trống = cùng origin (proxy /v1 của Vite)" />
      </label>
    </Modal>
  );
}
