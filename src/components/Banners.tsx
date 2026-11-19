import { useEffect, useState } from "react";
import { check, Update } from "@tauri-apps/plugin-updater";
import { relaunch } from "@tauri-apps/plugin-process";
import { AlertTriangle, ArrowDownCircle, X } from "lucide-react";
import { useApp } from "../state/store";
import { useT } from "../lib/strings";

// Баннер «Доступна версия …» — как UpdateBanner в приложении iOS/Mac, и
// плашка с ошибкой или предупреждением подключения.

let pendingUpdate: Update | null = null;

/** Проверка обновления; используется и баннером, и экраном «О приложении». */
export async function checkForUpdate(): Promise<Update | null> {
  const update = await check();
  pendingUpdate = update && update.available ? update : null;
  return pendingUpdate;
}

export function UpdateBanner() {
  const t = useT();
  const [update, setUpdate] = useState<{ version: string; notes?: string | null } | null>(null);
  const [state, setState] = useState<"idle" | "downloading" | "restarting" | "failed">("idle");

  useEffect(() => {
    const look = () => checkForUpdate()
      .then(u => setUpdate(u ? { version: u.version, notes: u.body } : null))
      .catch(() => {});
    look();
    // Приложение неделями висит в трее: проверка раз в шесть часов.
    const timer = setInterval(look, 6 * 60 * 60 * 1000);
    return () => clearInterval(timer);
  }, []);

  if (!update) return null;
  const install = async () => {
    setState("downloading");
    try {
      const u = pendingUpdate ?? (await checkForUpdate());
      if (!u) { setState("idle"); return; }
      await u.downloadAndInstall();
      setState("restarting");
      setTimeout(() => relaunch(), 1200);
    } catch {
      setState("failed");
    }
  };
  const statusText = state === "downloading" ? t("update.downloading")
    : state === "restarting" ? t("update.restarting")
    : state === "failed" ? t("update.failed") : null;

  return (
    <div className="card rise-in" style={{ padding: 14, display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <ArrowDownCircle size={26} strokeWidth={2.4} style={{ color: "var(--accent)", flexShrink: 0 }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 14, fontWeight: 700 }}>{t("update.available", { v: update.version })}</div>
          {update.notes && <div style={{ fontSize: 12, color: "var(--text-secondary)", marginTop: 2, lineHeight: 1.35 }}>{update.notes}</div>}
        </div>
        <button
          onClick={install}
          disabled={state === "downloading" || state === "restarting"}
          style={{ padding: "8px 16px", borderRadius: 999, background: "var(--accent)", color: "#fff", fontSize: 12, fontWeight: 700, opacity: state === "downloading" ? 0.6 : 1 }}
        >
          {t("update.action")}
        </button>
      </div>
      {statusText && <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>{statusText}</div>}
    </div>
  );
}

export function MessageBanner() {
  const t = useT();
  const banner = useApp(s => s.banner);
  const warning = useApp(s => (s.uiStatus === "connected" ? s.status.warning : undefined));
  const daemonDown = useApp(s => s.daemonDown);
  const dismiss = useApp(s => s.dismissBanner);
  const text = daemonDown ? t("error.daemon") : banner || warning;
  if (!text) return null;
  const isError = daemonDown || !!banner;
  const color = isError ? "var(--danger)" : "var(--pending)";
  return (
    <div className="rise-in" style={{
      display: "flex", gap: 10, alignItems: "flex-start", padding: "10px 12px", borderRadius: 12,
      background: `color-mix(in srgb, ${color} 12%, transparent)`, border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
    }}>
      <AlertTriangle size={16} style={{ color, flexShrink: 0, marginTop: 1 }} />
      <div className="selectable" style={{ flex: 1, fontSize: 12, lineHeight: 1.4, color: "var(--text-primary)" }}>{text}</div>
      {banner && !daemonDown && (
        <button onClick={dismiss} aria-label={t("common.close")} style={{ color: "var(--text-secondary)", display: "flex" }}><X size={14} /></button>
      )}
    </div>
  );
}
