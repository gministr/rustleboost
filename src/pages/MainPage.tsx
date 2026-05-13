import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { RefreshCw, Wifi, Globe } from "lucide-react";
import { useVPNStore } from "../store/vpnStore";
import ConnectionButton from "../components/ConnectionButton";
import ServerCard from "../components/ServerCard";
import { formatUptime, api } from "../api/daemon";

const stateLabels: Record<string, string> = {
  disconnected:  "Не подключено",
  connecting:    "Подключение...",
  connected:     "Защищено",
  disconnecting: "Отключение...",
};

const stateColors: Record<string, string> = {
  disconnected:  "var(--c-disconnected)",
  connecting:    "#60a5fa",
  connected:     "#4ade80",
  disconnecting: "var(--c-disconnected)",
};

export default function MainPage() {
  const {
    status, servers, connect, disconnect,
    error, clearError, updateSubscription, settings,
    fetchServers, pingAll,
  } = useVPNStore();
  const { state, server, stats } = status;

  const [refreshing, setRefreshing]   = useState(false);
  const [pinging, setPinging]         = useState(false);
  const [pingingId, setPingingId]     = useState<string | null>(null);

  const handleToggle = () => {
    if (state === "connected" || state === "connecting") disconnect();
    else {
      const target = server ?? servers[0];
      if (target) connect(target.id);
    }
  };

  const handleRefreshSub = async () => {
    setRefreshing(true);
    try { await updateSubscription(settings.subscription_url); } catch {}
    setRefreshing(false);
  };

  const handlePingAll = async () => {
    setPinging(true);
    await pingAll();
    setTimeout(async () => { await fetchServers(); setPinging(false); }, 3500);
  };

  const handlePingOne = async (serverId: string) => {
    setPingingId(serverId);
    try { await api.pingServer(serverId); await fetchServers(); }
    finally { setPingingId(null); }
  };

  const handleSelect = (serverId: string) => {
    if (state === "connected" && server?.id === serverId) disconnect();
    else connect(serverId);
  };

  return (
    <motion.div
      className="flex flex-col h-full overflow-hidden"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
    >
      {/* Error banner */}
      <AnimatePresence>
        {error && (
          <motion.div
            initial={{ opacity: 0, y: -8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}
            style={{
              margin: "8px 16px 0",
              padding: "10px 14px",
              borderRadius: 12,
              background: "rgba(239,68,68,0.1)",
              border: "1px solid rgba(239,68,68,0.25)",
              display: "flex", alignItems: "flex-start", gap: 8,
              flexShrink: 0,
            }}
          >
            <span style={{ color: "#f87171", fontSize: 12, flex: 1, lineHeight: 1.5 }}>{error}</span>
            <button onClick={clearError} style={{ color: "rgba(248,113,113,0.6)", fontSize: 12, background: "none", border: "none", cursor: "pointer" }}>✕</button>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Status + Button */}
      <div style={{ display: "flex", flexDirection: "column", alignItems: "center", paddingTop: 20, paddingBottom: 12, flexShrink: 0 }}>
        <p style={{ fontSize: 19, fontWeight: 700, marginBottom: 4, letterSpacing: "-0.02em", color: stateColors[state], transition: "color 0.3s" }}>
          {stateLabels[state]}
        </p>
        {state === "connected" && stats.uptime > 0 && (
          <p style={{ fontSize: 12, color: "var(--c-uptime)", fontFamily: "monospace", marginBottom: 4 }}>
            {formatUptime(stats.uptime)}
          </p>
        )}
        <div style={{ marginTop: 8, marginBottom: 4 }}>
          <ConnectionButton state={state} onClick={handleToggle} />
        </div>
      </div>

      {/* Servers section */}
      <div style={{ flex: 1, overflowY: "auto", padding: "0 12px 16px" }}>

        {/* Header */}
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 10, padding: "0 2px" }}>
          <p style={{
            fontSize: 11, fontWeight: 700, letterSpacing: "0.08em", textTransform: "uppercase",
            color: "var(--c-sec-label)",
          }}>
            Серверы
            {servers.length > 0 && (
              <span style={{ marginLeft: 6, fontWeight: 400 }}>{servers.length}</span>
            )}
          </p>
          <div style={{ display: "flex", gap: 6 }}>
            <IconBtn title="Проверить пинг" disabled={pinging || servers.length === 0} onClick={handlePingAll}>
              <Wifi size={13} style={{ animation: pinging ? "pulse 1s infinite" : "none" }} />
            </IconBtn>
            <IconBtn title="Обновить подписку" disabled={refreshing} onClick={handleRefreshSub}>
              <RefreshCw size={13} style={{ animation: refreshing ? "spin 1s linear infinite" : "none" }} />
            </IconBtn>
          </div>
        </div>

        {/* List */}
        {servers.length === 0 ? (
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", paddingTop: 48, gap: 10 }}>
            <Globe size={36} style={{ color: "var(--c-text-dimmer)", strokeWidth: 1 }} />
            <p style={{ fontSize: 13, color: "var(--c-text-dim)" }}>Нет серверов</p>
            <button onClick={handleRefreshSub} style={{ fontSize: 12, color: "#60a5fa", background: "none", border: "none", cursor: "pointer" }}>
              Обновить подписку
            </button>
          </div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {servers.map(srv => (
              <ServerCard
                key={srv.id}
                server={srv}
                isActive={server?.id === srv.id && (state === "connected" || state === "connecting")}
                isConnecting={server?.id === srv.id && state === "connecting"}
                onClick={() => handleSelect(srv.id)}
                onPing={() => handlePingOne(srv.id)}
                isPinging={pingingId === srv.id}
              />
            ))}
          </div>
        )}
      </div>

      <style>{`
        @keyframes spin   { to { transform: rotate(360deg); } }
        @keyframes pulse  { 0%,100% { opacity:1; } 50% { opacity:0.4; } }
      `}</style>
    </motion.div>
  );
}

function IconBtn({ children, onClick, disabled, title }: {
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        width: 30, height: 30,
        border: "1px solid var(--c-icon-btn-border)",
        borderRadius: 8,
        background: "var(--c-icon-btn-bg)",
        color: disabled ? "var(--c-text-dimmer)" : "var(--c-icon-btn-color)",
        cursor: disabled ? "not-allowed" : "pointer",
        display: "flex", alignItems: "center", justifyContent: "center",
        transition: "background 0.15s, color 0.15s",
      }}
      onMouseEnter={e => !disabled && (e.currentTarget.style.background = "var(--c-surface-hover)")}
      onMouseLeave={e => (e.currentTarget.style.background = "var(--c-icon-btn-bg)")}
    >
      {children}
    </button>
  );
}
