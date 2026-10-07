import { useEffect, useState, type ReactNode } from "react";
import { NavLink, Outlet, useLocation, useNavigate, useSearchParams } from "react-router-dom";

import { settings } from "../api/client";
import { useApp } from "./AppContext";
import { useAuth } from "./AuthContext";
import { CitationProvider } from "./Citation";
import { Empty, Icon, Menu } from "./ui";

const NAV: [string, string, string][] = [
  ["/documents", "folder_open", "Tài liệu"],
  ["/chat", "forum", "Hỏi đáp"],
  ["/cases", "account_tree", "Hồ sơ"],
];

// BePaylot's own mark (same as the favicon): a "b" monogram on a rounded tile.
export function Logo() {
  return (
    <svg viewBox="0 0 32 32" className="size-9" aria-hidden>
      <rect width="32" height="32" rx="9" className="fill-accent" />
      <path d="M11 7.7v16.6M11 7.7h5.6c2.4 0 4 1.6 4 4.2s-1.6 4.2-4 4.2H11M11 16.1h6.3c2.8 0 4.6 1.6 4.6 4.1s-1.8 4.1-4.6 4.1H11" fill="none" className="stroke-on-accent" strokeWidth="2.6" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

export function Layout() {
  const { kbs, kb, selectKB, connected, connError, openSettings, openCreateKB } = useApp();
  const { user, signOut } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  const [sp] = useSearchParams();
  const [drawer, setDrawer] = useState(false);
  const [q, setQ] = useState(sp.get("q") ?? "");

  useEffect(() => setDrawer(false), [loc.pathname]);
  useEffect(() => {
    if (loc.pathname === "/documents") setQ(sp.get("q") ?? "");
  }, [loc.pathname, sp]);

  const search = () => nav(`/documents${q.trim() ? "?q=" + encodeURIComponent(q.trim()) : ""}`);

  const navCls = ({ isActive }: { isActive: boolean }) =>
    "flex h-8 items-center gap-4 rounded-full pr-4 pl-4 text-sm " +
    (isActive ? "bg-accent-soft font-semibold text-on-accent-soft" : "text-fg hover:bg-fg/8");

  const sidebar = (
    <div className="flex h-full w-64 flex-col gap-1 overflow-y-auto pr-3 pb-3 pl-3">
      <Menu
        trigger={(open) => (
          <button onClick={open} className="mt-1 mb-4 flex h-14 w-fit items-center gap-3 rounded-2xl bg-surface pr-5 pl-4 text-sm font-medium text-fg shadow-1 transition-shadow hover:bg-accent-container/40 hover:shadow-2">
            <Icon name="add" size={24} />
            Mới
          </button>
        )}
        items={[
          { icon: "upload_file", label: "Tải file lên", onClick: () => nav("/documents?upload=1"), disabled: !kb },
          { icon: "create_new_folder", label: "Knowledge base mới", onClick: openCreateKB },
          { divider: true, label: "" },
          { icon: "chat_add_on", label: "Cuộc trò chuyện mới", onClick: () => nav("/chat") },
        ]}
      />
      <nav className="flex flex-col gap-0.5">
        {NAV.map(([to, icon, label]) => (
          <NavLink key={to} to={to} className={navCls}>
            {({ isActive }) => (
              <>
                <Icon name={icon} fill={isActive} size={20} />
                {label}
              </>
            )}
          </NavLink>
        ))}
      </nav>

      <div className="mt-4 flex items-center pr-1 pl-4">
        <span className="flex-1 text-xs font-medium text-muted">Knowledge base</span>
        <button className="btn-icon btn-sm" title="Knowledge base mới" onClick={openCreateKB}>
          <Icon name="add" size={20} />
        </button>
      </div>
      <div className="flex flex-col gap-0.5">
        {kbs.map((k) => {
          const on = k.id === kb?.id;
          return (
            <button
              key={k.id}
              onClick={() => selectKB(k.id)}
              title={k.description || k.name}
              className={"flex h-8 items-center gap-4 rounded-full pr-3 pl-4 text-left text-sm " + (on ? "bg-surface-3 font-medium text-fg" : "text-fg hover:bg-fg/8")}
            >
              <Icon name="folder_shared" fill={on} size={20} className={on ? "text-accent" : "text-muted"} />
              <span className="min-w-0 flex-1 truncate">{k.name}</span>
            </button>
          );
        })}
        {!kbs.length && connected && <div className="px-4 py-1 text-xs text-subtle">Chưa có knowledge base</div>}
      </div>

      <div className="flex-1" />
      <button onClick={() => openSettings("server")} className="mt-3 flex items-start gap-3 rounded-xl px-4 py-2 text-left hover:bg-fg/8" title={connError || settings.base}>
        <Icon name={connected === false ? "cloud_off" : "cloud_done"} size={20} className={connected === false ? "text-err" : "text-muted"} />
        <span className="min-w-0 text-xs text-muted">
          <span className="block font-medium text-fg">{connected === false ? "Mất kết nối" : connected ? "Đã kết nối" : "Đang kết nối…"}</span>
          <span className="block truncate">{settings.base || location.host}</span>
        </span>
      </button>
    </div>
  );

  return (
    <div className="flex h-full flex-col bg-bg">
      <header className="flex h-16 flex-none items-center gap-2 pr-3 pl-2 md:pl-4">
        <button className="btn-icon md:hidden" onClick={() => setDrawer(true)} aria-label="Menu">
          <Icon name="menu" />
        </button>
        <NavLink to="/documents" className="flex w-59 flex-none items-center gap-2.5 pl-1 text-[22px] text-muted max-md:w-auto">
          <Logo />
          <span className="max-sm:hidden">BePaylot</span>
        </NavLink>
        <form
          className="group flex h-12 max-w-180 min-w-0 flex-1 items-center gap-2 rounded-full bg-surface-3 px-2 transition-shadow focus-within:bg-surface focus-within:shadow-1"
          onSubmit={(e) => {
            e.preventDefault();
            search();
          }}
        >
          <button type="submit" className="btn-icon" aria-label="Tìm">
            <Icon name="search" />
          </button>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={kb ? `Tìm trong ${kb.name}` : "Tìm tài liệu"}
            className="min-w-0 flex-1 bg-transparent text-base text-fg outline-none placeholder:text-muted"
          />
          {q && (
            <button
              type="button"
              className="btn-icon"
              aria-label="Xoá"
              onClick={() => {
                setQ("");
                if (loc.pathname === "/documents") nav("/documents");
              }}
            >
              <Icon name="close" />
            </button>
          )}
        </form>
        <div className="flex-1 max-md:hidden" />
        <button className="btn-icon" title="Cài đặt: tài khoản, key và chi phí" onClick={() => openSettings("account")}>
          <Icon name="settings" />
        </button>
        <Menu
          align="right"
          trigger={(open) => (
            <button
              onClick={open}
              className="grid size-8 flex-none cursor-pointer place-items-center rounded-full bg-accent text-sm font-medium text-on-accent ring-offset-2 ring-offset-bg hover:ring-4 hover:ring-fg/8"
              title={user ? `${user.name}\n${user.email}` : "Tài khoản"}
            >
              {initial(user?.name || user?.email)}
            </button>
          )}
          items={[
            { icon: "account_circle", label: user?.email ?? "Tài khoản", disabled: true },
            { divider: true, label: "" },
            { icon: "manage_accounts", label: "Cài đặt", onClick: () => openSettings("account") },
            { icon: "account_balance_wallet", label: "Key & chi phí", onClick: () => openSettings("keys") },
            { icon: "logout", label: "Đăng xuất", onClick: signOut },
          ]}
        />
      </header>

      <div className="flex min-h-0 flex-1">
        <aside className="hidden md:block">{sidebar}</aside>
        {drawer && (
          <div className="fixed inset-0 z-700 md:hidden" onClick={() => setDrawer(false)}>
            <div className="absolute inset-0 bg-(--scrim)" />
            <div className="absolute inset-y-0 left-0 rounded-r-2xl bg-bg pt-4 shadow-3" onClick={(e) => e.stopPropagation()}>
              {sidebar}
            </div>
          </div>
        )}
        <main className="mr-0 mb-0 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-none bg-surface md:mr-4 md:mb-4 md:rounded-2xl">
          <CitationProvider>
            <Outlet />
          </CitationProvider>
        </main>
      </div>
    </div>
  );
}

const initial = (s?: string) => (s?.trim()[0] ?? "B").toUpperCase();

// Page header inside the content panel.
export function TopBar({ title, subtitle, children }: { title: ReactNode; subtitle?: ReactNode; kbSelector?: boolean; children?: ReactNode }) {
  return (
    <header className="flex min-h-16 flex-none flex-wrap items-center gap-2 px-6 pt-3 pb-2">
      <div className="min-w-0 flex-1">
        <h1 className="truncate text-[22px] leading-7 font-normal text-fg">{title}</h1>
        {subtitle && <div className="truncate text-xs text-muted">{subtitle}</div>}
      </div>
      <div className="flex flex-wrap items-center gap-1">{children}</div>
    </header>
  );
}

export function NeedKB() {
  const { connected, connError, openSettings, openCreateKB, loadingKBs } = useApp();
  if (loadingKBs) return null;
  if (connected === false)
    return (
      <Empty icon="cloud_off" title="Không kết nối được backend">
        <div className="text-xs">{connError}</div>
        <button className="btn btn-primary mt-5" onClick={() => openSettings("server")}>
          Cấu hình kết nối
        </button>
      </Empty>
    );
  return (
    <Empty icon="create_new_folder" title="Chưa có knowledge base">
      Tạo knowledge base đầu tiên để tải file lên và hỏi đáp.
      <div>
        <button className="btn btn-primary mt-5" onClick={openCreateKB}>
          <Icon name="add" size={18} /> Knowledge base mới
        </button>
      </div>
    </Empty>
  );
}
