import { useEffect, useState } from "react";
import { Check, Copy, Cpu, Info, KeyRound, LifeBuoy, MonitorSmartphone, Network, RefreshCw, Shield, Trash2, Zap } from "lucide-react";
import { SettingsGroup, SettingsRow, Segmented, Toggle, Spinner } from "../components/ui";
import { checkForUpdate } from "../components/Banners";
import { useApp } from "../state/store";
import { useT } from "../lib/strings";
import { api, Versions } from "../lib/api";

// Настройки — страница в том же окне, как на Mac (SettingsView), разделы
// и подписи те же. Выбора ядра и «прямого пути» больше нет: ядро одно.

function maskKey(url: string): string {
  try {
    const u = new URL(url);
    const tail = u.pathname.split("/").filter(Boolean).pop() ?? "";
    return `${u.host}/…${tail.slice(-6)}`;
  } catch {
    return url.length > 14 ? `${url.slice(0, 6)}…${url.slice(-6)}` : url;
  }
}

function Confirm({ title, text, action, onConfirm, onCancel }: { title: string; text: string; action: string; onConfirm: () => void; onCancel: () => void }) {
  const t = useT();
  return (
    <div className="fade-in" style={{ position: "fixed", inset: 0, zIndex: 60, background: "rgba(0,0,0,0.35)", display: "flex", alignItems: "center", justifyContent: "center", padding: 24 }}>
      <div className="card rise-in" style={{ maxWidth: 340, width: "100%", padding: 20, display: "flex", flexDirection: "column", gap: 10 }}>
        <div style={{ fontSize: 16, fontWeight: 800 }}>{title}</div>
        <div style={{ fontSize: 13, color: "var(--text-secondary)", lineHeight: 1.4 }}>{text}</div>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 6 }}>
          <button onClick={onCancel} style={{ padding: "8px 14px", borderRadius: 10, fontWeight: 700, color: "var(--text-secondary)", background: "var(--surface-elevated)" }}>{t("common.cancel")}</button>
          <button onClick={onConfirm} style={{ padding: "8px 14px", borderRadius: 10, fontWeight: 700, color: "#fff", background: "var(--danger)" }}>{action}</button>
        </div>
      </div>
    </div>
  );
}

export default function SettingsPage() {
  const t = useT();
  const settings = useApp(s => s.settings);
  const save = useApp(s => s.saveSettings);
  const theme = useApp(s => s.theme);
  const setTheme = useApp(s => s.setTheme);
  const mascot = useApp(s => s.mascot);
  const setMascot = useApp(s => s.setMascot);
  const setPage = useApp(s => s.setPage);
  const setEntryOpen = useApp(s => s.setEntryOpen);
  const removeSubscription = useApp(s => s.removeSubscription);
  const unread = useApp(s => s.inbox.unreadTickets + s.inbox.unreadNotices);

  const [hwid, setHwid] = useState("");
  const [copied, setCopied] = useState(false);
  const [versions, setVersions] = useState<Versions | null>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [updateState, setUpdateState] = useState<"idle" | "checking" | "latest" | "available" | "failed">("idle");
  const [updateVersion, setUpdateVersion] = useState("");

  useEffect(() => {
    api.hwid().then(h => setHwid(h.hwid)).catch(() => {});
    api.versions().then(setVersions).catch(() => {});
  }, []);

  const copyHwid = async () => {
    try { await navigator.clipboard.writeText(hwid); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch { /* */ }
  };
  const checkUpdate = async () => {
    setUpdateState("checking");
    try {
      const u = await checkForUpdate();
      if (u) { setUpdateVersion(u.version); setUpdateState("available"); } else setUpdateState("latest");
    } catch { setUpdateState("failed"); }
  };

  const routes = [
    { value: "all" as const, title: t("routing.off"), subtitle: t("routing.off.subtitle") },
    { value: "ru" as const, title: t("routing.preset.russia"), subtitle: t("routing.preset.russia.subtitle") },
    { value: "cn" as const, title: t("routing.preset.china"), subtitle: t("routing.preset.china.subtitle") },
  ];
  const ic = (Icon: typeof Zap) => <Icon size={18} strokeWidth={2.2} />;
  const intervals = [1, 3, 6, 12, 24];

  return (
    <div className="scroll" style={{ flex: 1, padding: "0 16px 32px" }}>
      <div style={{ maxWidth: 640, margin: "0 auto", display: "flex", flexDirection: "column", gap: 4 }}>
        <SettingsGroup title={t("settings.section.subscription")}>
          <SettingsRow icon={ic(KeyRound)} title={t("settings.subscriptionKey")} subtitle={<span className="selectable">{maskKey(settings.subscription_url)}</span>}
            trailing={<button onClick={() => setEntryOpen(true, "")} style={{ color: "var(--accent)", fontWeight: 700, fontSize: 13 }}>{t("settings.changeKey")}</button>} />
          <SettingsRow icon={ic(MonitorSmartphone)} title={t("settings.identity")} subtitle={<span className="selectable" style={{ fontFamily: "Consolas, monospace", fontSize: 11 }}>{hwid || "…"}</span>}
            trailing={hwid ? <button onClick={copyHwid} title={t("settings.copy")} style={{ color: "var(--accent)", display: "flex" }}>{copied ? <Check size={16} /> : <Copy size={16} />}</button> : undefined} />
          <SettingsRow icon={ic(Trash2)} title={t("settings.remove")} danger last onClick={() => setConfirmRemove(true)} />
        </SettingsGroup>

        <SettingsGroup title={t("settings.section.vpn")}>
          <SettingsRow icon={ic(Zap)} title={t("settings.tun")} subtitle={t("settings.tun.hint")}
            trailing={<Toggle checked={settings.tun_mode} onChange={v => save({ tun_mode: v })} />} />
          <SettingsRow icon={ic(Shield)} title={t("settings.killSwitch")} subtitle={t("settings.killSwitch.hint")}
            trailing={<Toggle checked={settings.kill_switch} disabled={!settings.tun_mode} onChange={v => save({ kill_switch: v })} />} />
          <SettingsRow icon={ic(Network)} title={t("settings.allowLan")} subtitle={t("settings.allowLan.hint")}
            trailing={<Toggle checked={settings.allow_lan} disabled={settings.tun_mode} onChange={v => save({ allow_lan: v })} />} />
          <SettingsRow icon={ic(Cpu)} title={t("settings.autoConnect")} subtitle={t("settings.autoConnect.hint")} last
            trailing={<Toggle checked={settings.auto_connect} onChange={v => save({ auto_connect: v })} />} />
        </SettingsGroup>

        <SettingsGroup title={t("settings.section.routing")} footer={t("routing.appliesNext")}>
          {routes.map((r, i) => (
            <SettingsRow key={r.value} title={r.title} subtitle={r.subtitle} last={i === routes.length - 1}
              onClick={() => save({ route_mode: r.value })}
              trailing={settings.route_mode === r.value ? <Check size={18} strokeWidth={2.6} style={{ color: "var(--accent)" }} /> : <span style={{ width: 18 }} />} />
          ))}
        </SettingsGroup>

        <SettingsGroup title={t("settings.section.subscriptionSettings")}>
          <SettingsRow icon={ic(RefreshCw)} title={t("settings.subscriptions.autoUpdate")}
            trailing={<Toggle checked={settings.auto_update} onChange={v => save({ auto_update: v })} />} />
          <SettingsRow title={t("settings.subscriptions.interval")} last trailing={
            <div style={{ width: 200 }}>
              <Segmented value={String(settings.update_interval)} onChange={v => save({ update_interval: Number(v) })}
                options={intervals.map(h => ({ value: String(h), label: String(h) }))} />
            </div>
          } />
        </SettingsGroup>

        <SettingsGroup title={t("settings.section.interface")}>
          <SettingsRow title={t("settings.theme")} trailing={
            <div style={{ width: 250 }}>
              <Segmented value={theme} onChange={setTheme} options={[
                { value: "system", label: t("settings.theme.system") },
                { value: "light", label: t("settings.theme.light") },
                { value: "dark", label: t("settings.theme.dark") },
              ]} />
            </div>
          } />
          <SettingsRow title={t("settings.mascot")} subtitle={t("settings.mascot.hint")}
            trailing={<Toggle checked={mascot} onChange={setMascot} />} />
          <SettingsRow title={t("settings.language")} last trailing={
            <div style={{ width: 170 }}>
              <Segmented value={settings.language} onChange={v => save({ language: v })} options={[
                { value: "ru", label: "Русский" }, { value: "en", label: "English" },
              ]} />
            </div>
          } />
        </SettingsGroup>

        <SettingsGroup title={t("settings.section.more")}>
          <SettingsRow icon={ic(LifeBuoy)} title={t("settings.support")} last onClick={() => setPage("support")}
            trailing={unread ? (
              <span style={{ minWidth: 20, height: 20, borderRadius: 10, padding: "0 6px", background: "var(--danger)", color: "#fff", fontSize: 11, fontWeight: 800, display: "flex", alignItems: "center", justifyContent: "center" }}>{unread}</span>
            ) : undefined} />
        </SettingsGroup>

        <SettingsGroup title={t("settings.section.about")}>
          <SettingsRow icon={ic(Info)} title={t("about.version.app")} trailing={<span style={{ color: "var(--text-secondary)", fontWeight: 600 }}>{versions?.app ?? "…"}</span>} />
          <SettingsRow title={t("about.version.xray")} trailing={<span style={{ color: "var(--text-secondary)", fontWeight: 600 }}>{versions?.xray ?? "…"}</span>} />
          <SettingsRow title={t("about.version.hysteria")} trailing={<span style={{ color: "var(--text-secondary)", fontWeight: 600 }}>{versions?.hysteria2 ?? "…"}</span>} />
          <SettingsRow title={t("about.update.check")} last onClick={checkUpdate}
            subtitle={updateState === "latest" ? t("about.update.upToDate")
              : updateState === "available" ? t("update.available", { v: updateVersion })
              : updateState === "failed" ? t("about.update.failed") : undefined}
            trailing={updateState === "checking" ? <Spinner /> : undefined} />
        </SettingsGroup>

        <div style={{ textAlign: "center", fontSize: 12, color: "var(--text-secondary)", padding: "18px 0 0" }}>
          <div style={{ fontWeight: 700 }}>RustleBoost — {t("about.tagline")}</div>
          <div className="selectable" style={{ marginTop: 4 }}>rustleboost.online</div>
        </div>
      </div>

      {confirmRemove && (
        <Confirm title={t("settings.remove.confirmTitle")} text={t("settings.remove.confirmText")} action={t("settings.remove.confirm")}
          onCancel={() => setConfirmRemove(false)}
          onConfirm={() => { setConfirmRemove(false); removeSubscription(); }} />
      )}
    </div>
  );
}
