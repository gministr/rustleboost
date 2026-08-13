import { useState } from "react";
import { motion } from "framer-motion";
import { ChevronRight, Wifi } from "lucide-react";
import { Server, transportLabel } from "../api/daemon";
import FlagImage, { stripNameEmoji } from "./FlagImage";

interface Props {
  server: Server;
  isActive: boolean;
  isConnecting: boolean;
  onClick: () => void;
  onPing?: () => void;
  isPinging?: boolean;
}

export default function ServerCard({
  server, isActive, isConnecting, onClick, onPing, isPinging,
}: Props) {
  const [hovered, setHovered] = useState(false);

  const lat = server.latency;
  // Thresholds allow for a full request through the tunnel, not a bare TCP
  // handshake: a healthy European node from Russia lands around 150-250 ms.
  const latColor = isPinging || lat < 0
    ? "rgba(255,255,255,0.2)"
    : lat < 250 ? "#4ade80"
    : lat < 500 ? "#facc15"
    : "#f87171";

  const displayName = stripNameEmoji(server.name);
  const transport = transportLabel(server);

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: 5 }}
      animate={{ opacity: 1, y: 0 }}
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        display: "flex", alignItems: "center", gap: 12,
        padding: "11px 14px", borderRadius: 14, cursor: "pointer",
        transition: "background 0.15s, border-color 0.15s",
        background: isActive
          ? "rgba(37,99,235,0.13)"
          : hovered ? "var(--c-surface-hover)"
          : "var(--c-surface)",
        border: `1px solid ${
          isActive ? "rgba(37,99,235,0.38)"
          : hovered ? "var(--c-border-hover)"
          : "var(--c-border)"}`,
      }}
    >
      {/* Circular flag */}
      <FlagImage flag={server.flag} name={server.name} country={server.country} size={44} />

      {/* Name + protocol */}
      <div style={{ flex: 1, minWidth: 0 }}>
        <p style={{
          fontSize: 14, fontWeight: 600, color: "var(--c-text)", margin: 0,
          lineHeight: 1.3, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
        }}>
          {displayName}
        </p>
        <p style={{
          fontSize: 11, color: "var(--c-text-sub)", margin: "2px 0 0",
          overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
        }}>
          {server.protocol}
          {transport && (
            <span style={{ color: "var(--c-text-dimmer)" }}> · {transport}</span>
          )}
        </p>
      </div>

      {/* Right side */}
      <div style={{ display: "flex", alignItems: "center", gap: 5, flexShrink: 0 }}>
        {isConnecting ? (
          <div style={{
            width: 14, height: 14, borderRadius: "50%",
            border: "2px solid #60a5fa", borderTopColor: "transparent",
            animation: "spin 0.7s linear infinite",
          }} />
        ) : (
          <span style={{ fontSize: 12, fontFamily: "monospace", minWidth: 38, textAlign: "right", color: latColor }}>
            {isPinging ? "..." : lat < 0 ? "—" : `${lat}ms`}
          </span>
        )}

        {hovered && onPing && !isPinging && !isConnecting && (
          <button
            onClick={e => { e.stopPropagation(); onPing(); }}
            style={{
              width: 22, height: 22, border: "none", borderRadius: 6,
              background: "var(--c-surface-hover)", cursor: "pointer",
              display: "flex", alignItems: "center", justifyContent: "center",
              color: "var(--c-text-sub)",
            }}
          >
            <Wifi size={11} />
          </button>
        )}

        <ChevronRight size={14} style={{
          color: isActive ? "rgba(96,165,250,0.6)" : "var(--c-text-dimmer)",
        }} />
      </div>

      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </motion.div>
  );
}
