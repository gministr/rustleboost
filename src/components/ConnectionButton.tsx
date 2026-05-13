import { motion, AnimatePresence } from "framer-motion";
import { Power, Loader2 } from "lucide-react";
import { ConnectionState } from "../api/daemon";

interface Props {
  state: ConnectionState;
  onClick: () => void;
}

export default function ConnectionButton({ state, onClick }: Props) {
  const isConnecting   = state === "connecting";
  const isConnected    = state === "connected";
  const isDisconnecting = state === "disconnecting";
  const isLoading      = isConnecting || isDisconnecting;

  // Button style per state
  const style: React.CSSProperties = isConnected
    ? {
        background: "linear-gradient(145deg, #16a34a, #15803d)",
        boxShadow: "0 0 44px rgba(22,163,74,0.4), 0 8px 32px rgba(22,163,74,0.25)",
      }
    : isConnecting
    ? {
        background: "linear-gradient(145deg, #2563eb, #1d4ed8)",
        boxShadow: "0 0 40px rgba(37,99,235,0.45), 0 8px 24px rgba(37,99,235,0.3)",
      }
    : {
        background: "var(--c-btn-bg)",
        boxShadow: "var(--c-btn-shadow)",
      };

  const ringColor = isConnected
    ? "rgba(22,163,74,0.35)"
    : isConnecting
    ? "rgba(37,99,235,0.4)"
    : "var(--c-btn-ring)";

  return (
    <div className="relative flex items-center justify-center">
      {/* Ripple rings for connected state */}
      <AnimatePresence>
        {isConnected && (
          <>
            {[0, 1, 2].map(i => (
              <motion.div
                key={i}
                className="absolute rounded-full"
                style={{
                  width: 120, height: 120,
                  border: "1px solid rgba(22,163,74,0.3)",
                }}
                initial={{ scale: 1, opacity: 0.7 }}
                animate={{ scale: 2.3, opacity: 0 }}
                transition={{ duration: 2.2, delay: i * 0.7, repeat: Infinity, ease: "easeOut" }}
              />
            ))}
          </>
        )}
        {isConnecting && (
          <>
            {[0, 1].map(i => (
              <motion.div
                key={i}
                className="absolute rounded-full"
                style={{
                  width: 120, height: 120,
                  border: "1px solid rgba(37,99,235,0.35)",
                }}
                initial={{ scale: 1, opacity: 0.6 }}
                animate={{ scale: 2, opacity: 0 }}
                transition={{ duration: 1.5, delay: i * 0.6, repeat: Infinity, ease: "easeOut" }}
              />
            ))}
          </>
        )}
      </AnimatePresence>

      {/* Main button */}
      <motion.button
        onClick={onClick}
        disabled={isLoading}
        whileTap={!isLoading ? { scale: 0.93 } : {}}
        whileHover={!isLoading ? { scale: 1.05 } : {}}
        transition={{ type: "spring", stiffness: 400, damping: 26 }}
        className="relative z-10 w-28 h-28 rounded-full flex items-center justify-center disabled:cursor-not-allowed"
        style={{
          ...style,
          outline: `3px solid ${ringColor}`,
          outlineOffset: 3,
          transition: "background 0.4s, box-shadow 0.4s, outline-color 0.4s",
        }}
      >
        <AnimatePresence mode="wait">
          {isLoading ? (
            <motion.div
              key="loader"
              initial={{ opacity: 0, scale: 0.5 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={{ opacity: 0, scale: 0.5 }}
            >
              <Loader2 size={38} className="text-white animate-spin" />
            </motion.div>
          ) : (
            <motion.div
              key="power"
              initial={{ opacity: 0, scale: 0.5 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={{ opacity: 0, scale: 0.5 }}
            >
              <Power
                size={38}
                strokeWidth={2}
                style={{ color: isConnected ? "white" : "var(--c-btn-icon)" }}
              />
            </motion.div>
          )}
        </AnimatePresence>
      </motion.button>
    </div>
  );
}
