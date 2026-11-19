import { useEffect, useState } from "react";
import { Power } from "lucide-react";
import planetUrl from "../assets/planet.webp";
import Mascot, { strideDuration } from "./Mascot";
import { useApp, UiStatus } from "../state/store";
import { useT } from "../lib/strings";

// Кнопка подключения, подпись состояния и талисман — как ConnectionSection,
// PowerButton, PlanetView и StatusLabel в приложении iOS/Mac.

const DIAMETER = 168;
const MASCOT_HEIGHT = 132;

/** Период оборота планеты: катится ровно со скоростью бега талисмана. */
const PLANET_PERIOD = ((Math.PI * DIAMETER) / MASCOT_HEIGHT) * strideDuration;

function tintOf(status: UiStatus): string {
  switch (status) {
    case "connected": return "var(--connected)";
    case "connecting":
    case "disconnecting": return "var(--pending)";
    case "failed": return "var(--danger)";
    default: return "var(--text-secondary)";
  }
}

function pad(n: number) { return String(n).padStart(2, "0"); }

function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  return `${pad(Math.floor(total / 3600))}:${pad(Math.floor((total % 3600) / 60))}:${pad(total % 60)}`;
}

function ConnectionTimer({ since }: { since: number }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick(x => x + 1), 1000);
    return () => clearInterval(t);
  }, []);
  return <span className="metric" style={{ fontSize: 28, fontWeight: 700, color: "var(--text-secondary)" }}>{formatDuration(Date.now() - since)}</span>;
}

export function StatusLabel() {
  const t = useT();
  const status = useApp(s => s.uiStatus);
  const since = useApp(s => s.connectedSince);
  const error = useApp(s => s.status.error);
  const measuring = useApp(s => s.measuring);
  const title = measuring ? t("status.pingChecking") : {
    connected: t("status.connected"),
    connecting: t("status.connecting"),
    disconnected: t("status.disconnected"),
    disconnecting: t("status.disconnecting"),
    failed: t("status.error"),
  }[status];
  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 4, textAlign: "center" }}>
      <div style={{ fontSize: 20, fontWeight: 700, color: "var(--text-primary)" }}>{title}</div>
      {/* Место под вторую строку занято всегда: иначе кнопка съезжала бы. */}
      <div style={{ minHeight: 38, display: "flex", alignItems: "center", justifyContent: "center", maxWidth: 360 }}>
        {status === "connected" && since !== null && <ConnectionTimer since={since} />}
        {status === "failed" && error && (
          <span className="selectable" style={{ fontSize: 12, color: "var(--danger)", lineHeight: 1.35 }}>{error}</span>
        )}
      </div>
    </div>
  );
}

function PowerButton({ status, showsPlanet, canConnect, onPress }: {
  status: UiStatus; showsPlanet: boolean; canConnect: boolean; onPress: () => void;
}) {
  const tint = tintOf(status);
  const transitioning = status === "connecting" || status === "disconnecting";
  const showsEarth = showsPlanet && status === "connected";
  const disabled = status === "disconnecting" || (!canConnect && !transitioning);
  const frame = DIAMETER + 28;
  return (
    <button
      onClick={onPress}
      disabled={disabled}
      aria-label={status === "connected" ? "Отключиться" : "Подключиться"}
      style={{ width: frame, height: frame, position: "relative", borderRadius: "50%", opacity: canConnect || transitioning ? 1 : 0.5 }}
    >
      {/* Ореол — только во время перехода. */}
      <span className="halo-pulse" style={{
        position: "absolute", inset: 0, borderRadius: "50%", border: `2px solid ${tint}`,
        opacity: transitioning ? 0.25 : 0, transition: "opacity 0.45s",
        animation: transitioning ? "halo-pulse 2s ease-in-out infinite" : "none",
      }} />
      <span style={{
        position: "absolute", left: 14, top: 14, width: DIAMETER, height: DIAMETER, borderRadius: "50%", overflow: "hidden",
        background: `color-mix(in srgb, ${tint} 12%, transparent)`, transition: "background 0.45s",
      }}>
        {showsEarth && (
          <span className="fade-in" style={{ position: "absolute", inset: 0 }}>
            <img src={planetUrl} alt="" draggable={false} className="planet-spin" style={{
              width: "100%", height: "100%", objectFit: "cover",
              animation: `planet-spin ${PLANET_PERIOD.toFixed(2)}s linear infinite`,
            }} />
            {/* Свет стоит на месте, шар крутится под ним. */}
            <span style={{
              position: "absolute", inset: 0,
              background: "radial-gradient(circle at 32% 28%, rgba(255,255,255,0.35), rgba(255,255,255,0) 45%), radial-gradient(circle at 38% 34%, transparent 35%, rgba(51,89,140,0.18) 75%)",
            }} />
          </span>
        )}
      </span>
      {!showsEarth && (
        <span style={{
          position: "absolute", left: 14, top: 14, width: DIAMETER, height: DIAMETER, borderRadius: "50%",
          border: `3px solid ${tint}`, transition: "border-color 0.45s",
        }} />
      )}
      <span style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center" }}>
        {showsEarth && (
          <span style={{
            position: "absolute", width: DIAMETER * 0.66, height: DIAMETER * 0.66, borderRadius: "50%",
            background: `radial-gradient(circle, rgba(255,255,255,0.35), rgba(255,255,255,0) ${DIAMETER * 0.28}px)`,
          }} />
        )}
        <Power size={56} strokeWidth={2.2} style={{
          position: "relative",
          color: showsEarth ? "var(--glyph-on-planet)" : tint,
          opacity: showsEarth ? 0.55 : 1, transition: "color 0.45s",
        }} />
      </span>
    </button>
  );
}

export default function ConnectionSection() {
  const status = useApp(s => s.uiStatus);
  const mascot = useApp(s => s.mascot);
  const canConnect = useApp(s => s.servers.length > 0);
  const toggle = useApp(s => s.toggleConnection);
  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 16, paddingTop: 8 }}>
      <StatusLabel />
      <div style={{ display: "flex", flexDirection: "column", alignItems: "center" }}>
        {mascot && <div style={{ marginBottom: -20, zIndex: 1 }}><Mascot status={status} height={MASCOT_HEIGHT} /></div>}
        <PowerButton status={status} showsPlanet={mascot} canConnect={canConnect} onPress={toggle} />
      </div>
    </div>
  );
}
