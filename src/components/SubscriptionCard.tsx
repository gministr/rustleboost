import { motion } from "framer-motion";
import { CalendarClock, Gauge } from "lucide-react";
import { SubscriptionInfo, formatBytes, formatExpiry, daysLeft } from "../api/daemon";
import { useT } from "../i18n";

interface Props {
  info: SubscriptionInfo;
}

/**
 * Traffic and validity for the active subscription, taken from the panel's own
 * headers rather than measured locally — these are the figures the user is
 * billed against.
 */
export default function SubscriptionCard({ info }: Props) {
  const t = useT();
  const used = info.upload + info.download;
  const unlimited = info.total <= 0;
  const ratio = unlimited ? 0 : Math.min(used / info.total, 1);
  const left = daysLeft(info.expire);

  // Warn only once the subscription is genuinely close to running out.
  const trafficLow = !unlimited && ratio >= 0.9;
  const expirySoon = left !== null && left <= 7;

  const barColor = trafficLow ? "#f87171" : ratio >= 0.75 ? "#facc15" : "#4ade80";

  return (
    <motion.div
      initial={{ opacity: 0, y: -6 }}
      animate={{ opacity: 1, y: 0 }}
      style={{
        margin: "8px 12px 0",
        padding: "10px 13px",
        borderRadius: 14,
        background: "var(--c-surface)",
        border: "1px solid var(--c-border)",
        flexShrink: 0,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 7 }}>
        <Gauge size={13} style={{ color: "var(--c-text-sub)", flexShrink: 0 }} />
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--c-text)" }}>
          {formatBytes(used)}
        </span>
        <span style={{ fontSize: 11, color: "var(--c-text-dim)" }}>
          {unlimited ? "/ ∞" : `/ ${formatBytes(info.total)}`}
        </span>

        <div style={{ flex: 1 }} />

        <CalendarClock
          size={12}
          style={{ color: expirySoon ? "#facc15" : "var(--c-text-dimmer)", flexShrink: 0 }}
        />
        <span style={{ fontSize: 11, color: expirySoon ? "#facc15" : "var(--c-text-dim)" }}>
          {info.expire ? formatExpiry(info.expire) : t("unlimited")}
        </span>
      </div>

      {!unlimited && (
        <div style={{
          height: 4, borderRadius: 3, overflow: "hidden",
          background: "var(--c-surface-hover)",
        }}>
          <motion.div
            initial={{ width: 0 }}
            animate={{ width: `${ratio * 100}%` }}
            transition={{ duration: 0.5, ease: "easeOut" }}
            style={{ height: "100%", background: barColor, borderRadius: 3 }}
          />
        </div>
      )}

      {expirySoon && left !== null && (
        <p style={{ fontSize: 10, color: "#facc15", margin: "6px 0 0" }}>
          {left <= 0 ? t("expired") : `${t("daysLeft")}: ${left}`}
        </p>
      )}
    </motion.div>
  );
}
