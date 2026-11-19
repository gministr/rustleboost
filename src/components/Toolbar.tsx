import { useEffect, useRef, useState, ReactNode } from "react";
import { ArrowLeft, ClipboardPaste, Keyboard, Minus, Plus, Settings, X } from "lucide-react";
import { getCurrentWindow } from "@tauri-apps/api/window";
import Wordmark from "./Wordmark";
import { ToolbarButton } from "./ui";
import { useApp } from "../state/store";
import { useT } from "../lib/strings";

// Панель окна — как панель инструментов приложения на Mac: шестерёнка слева,
// словесный знак по центру, «+» справа. Окно без системной рамки, поэтому
// панель же и перетаскивает окно, а справа — кнопки Windows «свернуть» и
// «закрыть» (закрытие прячет окно в трей).

function WindowButtons() {
  const t = useT();
  const style = { width: 30, height: 30, borderRadius: 8, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-secondary)" };
  return (
    <div className="no-drag" style={{ display: "flex", gap: 2, marginLeft: 6 }}>
      <button title={t("common.minimize")} style={style} className="row-hover" onClick={() => getCurrentWindow().minimize()}><Minus size={14} /></button>
      <button title={t("common.closeWindow")} style={style} className="win-close" onClick={() => getCurrentWindow().hide()}><X size={14} /></button>
    </div>
  );
}

function AddMenu() {
  const t = useT();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const setEntryOpen = useApp(s => s.setEntryOpen);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setOpen(false); };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);

  const paste = async () => {
    setOpen(false);
    let text = "";
    try { text = (await navigator.clipboard.readText()).trim(); } catch { /* нет доступа к буферу */ }
    setEntryOpen(true, text);
  };
  const item = (icon: ReactNode, label: string, onClick: () => void) => (
    <button onClick={onClick} className="menu-item" style={{ width: "100%", display: "flex", alignItems: "center", gap: 10, padding: "9px 12px", borderRadius: 8, fontSize: 13, fontWeight: 600, textAlign: "left" }}>
      <span style={{ color: "var(--accent)", display: "flex" }}>{icon}</span>{label}
    </button>
  );
  return (
    <div ref={ref} style={{ position: "relative" }} className="no-drag">
      <ToolbarButton title={t("addMenu.action")} onClick={() => setOpen(o => !o)}><Plus size={17} strokeWidth={2.4} /></ToolbarButton>
      {open && (
        <div className="card rise-in" style={{ position: "absolute", right: 0, top: 40, width: 220, padding: 6, zIndex: 50 }}>
          {item(<ClipboardPaste size={16} />, t("addMenu.paste"), paste)}
          {item(<Keyboard size={16} />, t("addMenu.manual"), () => { setOpen(false); setEntryOpen(true, ""); })}
        </div>
      )}
    </div>
  );
}

export default function Toolbar({ title, onBack }: { title?: string; onBack?: () => void }) {
  const t = useT();
  const setPage = useApp(s => s.setPage);
  const unread = useApp(s => s.inbox.unreadTickets + s.inbox.unreadNotices);
  // До ввода ключа на панели только знак: настройки без подписки пусты.
  const minimal = useApp(s => !s.settings.subscription_url);
  return (
    <div className="drag" style={{ height: 56, flexShrink: 0, display: "flex", alignItems: "center", gap: 8, padding: "0 10px 0 14px", position: "relative" }}>
      {minimal ? null : onBack ? (
        <button onClick={onBack} title={t("common.back")} aria-label={t("common.back")} className="no-drag row-hover"
          style={{ width: 36, height: 32, borderRadius: 6, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-primary)" }}>
          <ArrowLeft size={18} strokeWidth={2} />
        </button>
      ) : (
        <ToolbarButton title={t("settings.title")} onClick={() => setPage("settings")} badge={unread}><Settings size={17} strokeWidth={2.2} /></ToolbarButton>
      )}
      <div style={{ position: "absolute", left: 0, right: 0, display: "flex", justifyContent: "center", pointerEvents: "none" }}>
        {title ? <span style={{ fontSize: 16, fontWeight: 800 }}>{title}</span> : <Wordmark size={20} />}
      </div>
      <span style={{ flex: 1 }} />
      {!onBack && !minimal && <AddMenu />}
      <WindowButtons />
    </div>
  );
}
