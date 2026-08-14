import { useState, useEffect } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { RefreshCw, CheckCircle2, AlertCircle, Copy, Cpu } from "lucide-react";
import { check } from "@tauri-apps/plugin-updater";
import { relaunch } from "@tauri-apps/plugin-process";
import { useVPNStore } from "../store/vpnStore";
import { Settings, api, HWIDInfo } from "../api/daemon";
import { useT, formatHours, Language } from "../i18n";

type ReqStatus = "idle" | "loading" | "success" | "error";

/** Kept in step with package.json and tauri.conf.json on every release. */
const APP_VERSION = "1.3.3";

/* ── Toggle ─────────────────────────────────────────────────────────── */
function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      onClick={() => onChange(!checked)}
      style={{
        width: 44, height: 24,
        borderRadius: 12,
        backgroundColor: checked ? "#0ea5e9" : "var(--c-titlebar-btn)",
        position: "relative",
        flexShrink: 0,
        border: "none",
        cursor: "pointer",
        transition: "background-color 0.2s",
      }}
    >
      <div style={{
        position: "absolute",
        top: 2, left: checked ? 22 : 2,
        width: 20, height: 20,
        borderRadius: 10,
        backgroundColor: "white",
        boxShadow: "0 1px 4px rgba(0,0,0,0.3)",
        transition: "left 0.2s",
      }} />
    </button>
  );
}

/* ── Section label ───────────────────────────────────────────────────── */
function Sec({ title }: { title: string }) {
  return (
    <p style={{
      fontSize: 11, letterSpacing: "0.1em", textTransform: "uppercase",
      color: "var(--c-sec-label)", fontWeight: 600,
      paddingTop: 20, paddingBottom: 8,
    }}>{title}</p>
  );
}

/* ── Row ─────────────────────────────────────────────────────────────── */
function Row({ icon: Icon, label, sub, children }: {
  icon: React.ElementType; label: string; sub?: string; children: React.ReactNode;
}) {
  return (
    <div style={{
      display: "flex", alignItems: "center", gap: 12,
      padding: "14px 16px",
      background: "var(--c-surface)",
      border: "1px solid var(--c-border)",
      borderRadius: 14, marginBottom: 8,
    }}>
      <div style={{
        width: 36, height: 36, borderRadius: 10,
        background: "var(--c-surface-hover)",
        display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0,
      }}>
        <Icon size={16} style={{ color: "var(--c-text-sub)" }} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <p style={{ fontSize: 14, color: "var(--c-text)", margin: 0, fontWeight: 500 }}>{label}</p>
        {sub && <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "2px 0 0" }}>{sub}</p>}
      </div>
      {children}
    </div>
  );
}

export default function SettingsPage() {
  const { settings, updateSubscription, saveSettings } = useVPNStore();
  const [subURL, setSubURL] = useState(settings.subscription_url ?? "");
  const [subStatus, setSubStatus] = useState<ReqStatus>("idle");
  const [subMsg, setSubMsg] = useState("");
  const t = useT();
  const [hwid, setHwid] = useState<HWIDInfo | null>(null);
  const [appUpdate, setAppUpdate] = useState<"idle" | "checking" | "downloading">("idle");
  const [appUpdateMsg, setAppUpdateMsg] = useState(`RustleBoost v${APP_VERSION}`);

  // Downloads and installs straight away: a user who pressed "Проверить"
  // has already said yes to updating.
  const checkAppUpdate = async () => {
    setAppUpdate("checking");
    setAppUpdateMsg(t("checking"));
    try {
      const update = await check();
      if (!update?.available) {
        setAppUpdate("idle");
        setAppUpdateMsg(`${t("upToDate")} — v${APP_VERSION}`);
        return;
      }
      setAppUpdate("downloading");
      setAppUpdateMsg(`${t("downloadingVersion")} v${update.version}...`);
      await update.downloadAndInstall();
      setAppUpdateMsg(t("restarting"));
      setTimeout(() => relaunch(), 1500);
    } catch {
      setAppUpdate("idle");
      setAppUpdateMsg(t("updateFailed"));
    }
  };
  const [copied, setCopied] = useState(false);

  useEffect(() => { setSubURL(settings.subscription_url ?? ""); }, [settings.subscription_url]);
  useEffect(() => { api.getHWID().then(setHwid).catch(() => {}); }, []);

  const handleUpdateSub = async () => {
    const url = subURL.trim();
    if (!url) return;
    setSubStatus("loading"); setSubMsg("");
    try {
      await updateSubscription(url);
      setSubStatus("success"); setSubMsg(t("saved"));
      setTimeout(() => setSubStatus("idle"), 3000);
    } catch (e: any) {
      setSubStatus("error"); setSubMsg(e?.message ?? t("errSubscription"));
      setTimeout(() => setSubStatus("idle"), 4000);
    }
  };

  const copyHWID = () => {
    if (!hwid?.hwid) return;
    navigator.clipboard.writeText(hwid.hwid).then(() => {
      setCopied(true); setTimeout(() => setCopied(false), 2000);
    });
  };

  const toggle = (key: keyof Settings) => (val: boolean) =>
    saveSettings({ [key]: val } as Partial<Settings>);
  const pick = (key: keyof Settings) => (val: string) =>
    saveSettings({ [key]: val } as Partial<Settings>);

  // Icons inline (avoiding import issues)
  const icons = {
    bolt: () => (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
      </svg>
    ),
    shield: () => (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
      </svg>
    ),
    link: () => (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
    ),
    globe: () => (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/>
        <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
      </svg>
    ),
    refresh: () => (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/>
        <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
      </svg>
    ),
    lang: () => (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/>
        <path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="M14 18h6"/>
      </svg>
    ),
  };

  const selectStyle: React.CSSProperties = {
    background: "var(--c-surface-hover)",
    border: "1px solid var(--c-border)",
    borderRadius: 10,
    padding: "6px 10px",
    fontSize: 13,
    color: "var(--c-text)",
    outline: "none",
    cursor: "pointer",
    flexShrink: 0,
  };

  return (
    <motion.div
      style={{ display: "flex", flexDirection: "column", height: "100%" }}
      initial={{ opacity: 0, x: -20 }}
      animate={{ opacity: 1, x: 0 }}
      exit={{ opacity: 0, x: -20 }}
    >
      <div style={{ padding: "8px 16px 4px", flexShrink: 0 }}>
        <h2 style={{ fontSize: 16, fontWeight: 600, margin: 0 }}>{t("navSettings")}</h2>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "0 16px 24px" }}>

        {/* ── Подписка ── */}
        <Sec title={t("secSubscription")} />
        <div style={{
          background: "var(--c-surface)",
          border: "1px solid rgba(255,255,255,0.07)",
          borderRadius: 14, padding: 16, marginBottom: 8,
        }}>
          <p style={{ fontSize: 12, color: "var(--c-text-sub)", margin: "0 0 10px" }}>
            {t("subscriptionKey")}
          </p>
          <div style={{ display: "flex", gap: 8 }}>
            <input
              type="text"
              value={subURL}
              onChange={e => setSubURL(e.target.value)}
              onKeyDown={e => e.key === "Enter" && handleUpdateSub()}
              placeholder="https://sub.lindavpn.com/token"
              style={{
                flex: 1, padding: "10px 12px",
                fontSize: 13, borderRadius: 10,
                background: "var(--c-border)",
                border: "1px solid rgba(255,255,255,0.12)",
                color: "white", outline: "none",
                WebkitUserSelect: "text",
              }}
            />
            <button
              onClick={handleUpdateSub}
              disabled={subStatus === "loading" || !subURL.trim()}
              style={{
                width: 42, height: 42, borderRadius: 10, flexShrink: 0,
                background: subStatus === "loading" || !subURL.trim() ? "rgba(14,165,233,0.4)" : "#0ea5e9",
                border: "none", cursor: "pointer", color: "white",
                display: "flex", alignItems: "center", justifyContent: "center",
                transition: "background 0.2s",
              }}
            >
              <RefreshCw size={15} style={{ animation: subStatus === "loading" ? "spin 1s linear infinite" : "none" }} />
            </button>
          </div>
          <AnimatePresence>
            {subMsg && (
              <motion.p
                initial={{ opacity: 0, y: -4 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}
                style={{
                  display: "flex", alignItems: "center", gap: 6,
                  marginTop: 10, fontSize: 12, fontWeight: 500,
                  color: subStatus === "success" ? "#4ade80" : "#f87171",
                }}
              >
                {subStatus === "success" ? <CheckCircle2 size={12} /> : <AlertCircle size={12} />}
                {subMsg}
              </motion.p>
            )}
          </AnimatePresence>
        </div>

        {/* ── HWID ── */}
        <Sec title={t("secDevice")} />
        <div style={{
          background: "var(--c-surface)",
          border: "1px solid rgba(255,255,255,0.07)",
          borderRadius: 14, padding: 16, marginBottom: 8,
        }}>
          {hwid ? (
            <>
              <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 10 }}>
                <code style={{
                  flex: 1, padding: "10px 12px", borderRadius: 10,
                  background: "var(--c-code-bg)", border: "1px solid var(--c-code-border)",
                  fontFamily: "monospace", fontSize: 12, color: "var(--c-code-text)",
                  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
                  WebkitUserSelect: "text",
                }}>
                  {hwid.hwid}
                </code>
                <button
                  onClick={copyHWID}
                  style={{
                    width: 40, height: 40, borderRadius: 10, flexShrink: 0,
                    background: "var(--c-border)", border: "1px solid rgba(255,255,255,0.1)",
                    color: copied ? "#4ade80" : "rgba(255,255,255,0.5)",
                    cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center",
                    transition: "color 0.2s",
                  }}
                >
                  {copied ? <CheckCircle2 size={15} /> : <Copy size={15} />}
                </button>
              </div>
              <div style={{ display: "flex", gap: 16, fontSize: 11, color: "var(--c-text-dim)", marginBottom: 10 }}>
                <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
                  <Cpu size={11} /> {hwid.model}
                </span>
                <span>{hwid.os} {hwid.ver}</span>
              </div>
              <p style={{ fontSize: 11, color: "var(--c-text-dim)", lineHeight: 1.5, margin: 0 }}>
                {t("deviceHint")}
              </p>
            </>
          ) : (
            <p style={{ fontSize: 12, color: "var(--c-text-dim)" }}>{t("loading")}</p>
          )}
        </div>

        {/* ── Маршрутизация ── */}
        <Sec title={t("secRouting")} />
        <div style={{
          background: "var(--c-surface)",
          border: "1px solid rgba(255,255,255,0.07)",
          borderRadius: 14, padding: 16, marginBottom: 8,
        }}>
          <p style={{ fontSize: 12, color: "var(--c-text-sub)", margin: "0 0 12px" }}>
            {t("routingMode")}
          </p>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            {([
              { value: "all", labelKey: "routeAll", subKey: "routeAllSub" },
              { value: "ru",  labelKey: "routeRu",  subKey: "routeRuSub" },
              { value: "cn",  labelKey: "routeCn",  subKey: "routeCnSub" },
            ] as const).map(({ value, labelKey, subKey }) => {
              const active = settings.route_mode === value;
              return (
                <button
                  key={value}
                  onClick={() => saveSettings({ route_mode: value })}
                  style={{
                    display: "flex", alignItems: "center", gap: 12,
                    padding: "10px 14px", borderRadius: 10, cursor: "pointer",
                    background: active ? "rgba(14,165,233,0.15)" : "rgba(255,255,255,0.04)",
                    border: `1px solid ${active ? "rgba(14,165,233,0.45)" : "var(--c-surface-hover)"}`,
                    transition: "all 0.15s", textAlign: "left",
                  }}
                >
                  <div style={{
                    width: 16, height: 16, borderRadius: "50%", flexShrink: 0,
                    border: `2px solid ${active ? "#0ea5e9" : "var(--c-text-dim)"}`,
                    background: active ? "#0ea5e9" : "transparent",
                    display: "flex", alignItems: "center", justifyContent: "center",
                  }}>
                    {active && <div style={{ width: 6, height: 6, borderRadius: "50%", background: "white" }} />}
                  </div>
                  <div style={{ flex: 1 }}>
                    <p style={{ fontSize: 13, fontWeight: 600, color: active ? "#38bdf8" : "var(--c-text)", margin: 0 }}>{t(labelKey)}</p>
                    <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "2px 0 0" }}>{t(subKey)}</p>
                  </div>
                </button>
              );
            })}
          </div>
          <p style={{ fontSize: 11, color: "var(--c-text-dim)", marginTop: 10, lineHeight: 1.5 }}>
            {t("appliesNextConnect")}
          </p>
        </div>

        {/* ── Ядро подключения ── */}
        <Sec title={t("secRouter")} />
        <div style={{
          background: "var(--c-surface)",
          border: "1px solid rgba(255,255,255,0.07)",
          borderRadius: 14, padding: 16, marginBottom: 8,
        }}>
          <p style={{ fontSize: 12, color: "var(--c-text-sub)", margin: "0 0 12px", lineHeight: 1.5 }}>
            {t("routerHint")}
          </p>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            {([
              { value: "auto",    labelKey: "routerAuto",    subKey: "routerAutoSub" },
              { value: "singbox", labelKey: "routerSingBox", subKey: "routerSingBoxSub" },
              { value: "xray",    labelKey: "routerXray",    subKey: "routerXraySub" },
            ] as const).map(({ value, labelKey, subKey }) => {
              const active = settings.router_mode === value;
              return (
                <button
                  key={value}
                  onClick={() => saveSettings({ router_mode: value })}
                  style={{
                    display: "flex", alignItems: "center", gap: 12,
                    padding: "10px 14px", borderRadius: 10, cursor: "pointer",
                    background: active ? "rgba(14,165,233,0.15)" : "rgba(255,255,255,0.04)",
                    border: `1px solid ${active ? "rgba(14,165,233,0.45)" : "var(--c-surface-hover)"}`,
                    transition: "all 0.15s", textAlign: "left",
                  }}
                >
                  <div style={{
                    width: 16, height: 16, borderRadius: "50%", flexShrink: 0,
                    border: `2px solid ${active ? "#0ea5e9" : "var(--c-text-dim)"}`,
                    background: active ? "#0ea5e9" : "transparent",
                    display: "flex", alignItems: "center", justifyContent: "center",
                  }}>
                    {active && <div style={{ width: 6, height: 6, borderRadius: "50%", background: "white" }} />}
                  </div>
                  <div style={{ flex: 1 }}>
                    <p style={{ fontSize: 13, fontWeight: 600, color: active ? "#38bdf8" : "var(--c-text)", margin: 0 }}>{t(labelKey)}</p>
                    <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "2px 0 0" }}>{t(subKey)}</p>
                  </div>
                </button>
              );
            })}
          </div>
          <p style={{ fontSize: 11, color: "var(--c-text-dim)", marginTop: 10, lineHeight: 1.5 }}>
            {t("appliesNextConnect")}
          </p>
        </div>

        {/* ── Подключение ── */}
        <Sec title={t("secConnection")} />

        <Row icon={icons.bolt} label={t("tunMode")} sub={t("tunModeSub")}>
          <Toggle checked={settings.tun_mode} onChange={toggle("tun_mode")} />
        </Row>
        <Row icon={icons.shield} label={t("killSwitch")} sub={t("killSwitchSub")}>
          <Toggle checked={settings.kill_switch} onChange={toggle("kill_switch")} />
        </Row>
        <Row icon={icons.bolt} label={t("autoConnect")} sub={t("autoConnectSub")}>
          <Toggle checked={settings.auto_connect} onChange={toggle("auto_connect")} />
        </Row>

        {/* ── Обновления ── */}
        <Sec title={t("secUpdates")} />
        <Row icon={icons.refresh} label={t("autoUpdateSubscription")}>
          <Toggle checked={settings.auto_update} onChange={toggle("auto_update")} />
        </Row>
        <Row icon={icons.refresh} label={t("interval")}>
          <select value={settings.update_interval} onChange={e => saveSettings({ update_interval: +e.target.value })} style={selectStyle}>
            {[1, 3, 6, 12, 24].map(h => <option key={h} value={h}>{formatHours(settings.language as Language, h)}</option>)}
          </select>
        </Row>
        <Row icon={icons.refresh} label={t("appVersion")} sub={appUpdateMsg}>
          <button
            onClick={checkAppUpdate}
            disabled={appUpdate !== "idle"}
            style={{
              padding: "6px 12px", borderRadius: 8, fontSize: 12, fontWeight: 600,
              background: "var(--c-border)", border: "1px solid rgba(255,255,255,0.1)",
              color: "var(--c-text-sub)",
              cursor: appUpdate === "idle" ? "pointer" : "default",
              whiteSpace: "nowrap",
            }}
          >
            {appUpdate === "checking" ? t("checking")
              : appUpdate === "downloading" ? t("updateDownloading")
              : t("checkUpdates")}
          </button>
        </Row>

        {/* ── Язык ── */}
        <Sec title={t("secInterface")} />
        <Row icon={icons.lang} label={t("language")}>
          <select value={settings.language} onChange={e => pick("language")(e.target.value)} style={selectStyle}>
            <option value="ru">Русский</option>
            <option value="en">English</option>
          </select>
        </Row>

        <p style={{ textAlign: "center", fontSize: 10, color: "var(--c-titlebar-btn)", marginTop: 24 }}>
          RustleBoost v{APP_VERSION} · sing-box + xray
        </p>
      </div>

      <style>{`@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }`}</style>
    </motion.div>
  );
}

