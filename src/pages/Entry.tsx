import { useEffect, useState } from "react";
import { ClipboardPaste, X } from "lucide-react";
import logoLight from "../assets/logo-light.webp";
import logoDark from "../assets/logo-dark.webp";
import { PrimaryButton } from "../components/ui";
import { useApp } from "../state/store";
import { useT } from "../lib/strings";
import { useIsDark } from "../lib/theme";

// Ввод ключа подписки — как SubscriptionEntryView в приложении iOS/Mac.
// Показывается при первом запуске и по «+» / «Изменить» в настройках.

export default function Entry({ asSheet }: { asSheet?: boolean }) {
  const t = useT();
  const isDark = useIsDark();
  const prefill = useApp(s => s.entryPrefill);
  const setSubscription = useApp(s => s.setSubscription);
  const close = useApp(s => s.setEntryOpen);
  const loading = useApp(s => s.refreshing);
  const [value, setValue] = useState(prefill);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setValue(prefill), [prefill]);

  const paste = async () => {
    try { setValue((await navigator.clipboard.readText()).trim()); } catch { /* нет доступа */ }
  };
  const submit = async () => {
    setError(null);
    try { await setSubscription(value); }
    catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  };

  const body = (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 14, width: "100%", maxWidth: 380 }}>
      {/* Файлы названы по теме: logo-dark — для тёмной (светлая собака). */}
      <img src={isDark ? logoDark : logoLight} alt="" draggable={false} style={{ height: 110, objectFit: "contain" }} />
      <div style={{ fontSize: 22, fontWeight: 800, textAlign: "center" }}>{asSheet ? t("entry.changeTitle") : t("entry.title")}</div>
      <div style={{ fontSize: 13, color: "var(--text-secondary)", textAlign: "center", lineHeight: 1.4 }}>
        {asSheet ? t("entry.changeSubtitle") : t("entry.subtitle")}
      </div>
      <textarea
        value={value}
        onChange={e => setValue(e.target.value)}
        onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); if (value.trim()) submit(); } }}
        placeholder={t("entry.placeholder")}
        rows={3}
        autoFocus
        spellCheck={false}
        style={{
          width: "100%", resize: "none", padding: "12px 14px", borderRadius: 16, outline: "none",
          background: "var(--surface-elevated)", border: "1px solid var(--separator)", fontSize: 13, lineHeight: 1.4,
          fontFamily: "Consolas, 'Cascadia Mono', monospace",
        }}
      />
      <button onClick={paste} style={{ display: "flex", alignItems: "center", gap: 6, color: "var(--accent)", fontSize: 13, fontWeight: 700 }}>
        <ClipboardPaste size={15} />{t("entry.paste")}
      </button>
      {error && <div className="selectable" style={{ fontSize: 12, color: "var(--danger)", textAlign: "center", lineHeight: 1.4 }}>{error}</div>}
      <PrimaryButton onClick={submit} disabled={!value.trim()} loading={loading}>{t("entry.continue")}</PrimaryButton>
    </div>
  );

  if (!asSheet) {
    return <div className="scroll" style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", padding: "8px 24px 32px" }}>{body}</div>;
  }
  return (
    <div className="fade-in" style={{ position: "absolute", inset: 0, zIndex: 40, background: "rgba(0,0,0,0.35)", display: "flex", alignItems: "center", justifyContent: "center", padding: 16 }}
      onMouseDown={e => { if (e.target === e.currentTarget) close(false); }}>
      <div className="card rise-in" style={{ position: "relative", padding: "28px 24px 24px", width: "100%", maxWidth: 440, display: "flex", justifyContent: "center" }}>
        <button onClick={() => close(false)} aria-label={t("common.cancel")} style={{ position: "absolute", top: 12, right: 12, color: "var(--text-secondary)", display: "flex" }}><X size={18} /></button>
        {body}
      </div>
    </div>
  );
}
