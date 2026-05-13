import { useState } from "react";
import { motion } from "framer-motion";
import { ArrowRight, RefreshCw, AlertCircle } from "lucide-react";
import { useVPNStore } from "../store/vpnStore";
import logoUrl from "../assets/logo.png";

export default function OnboardingPage() {
  const { updateSubscription } = useVPNStore();
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async () => {
    const trimmed = url.trim();
    if (!trimmed) return;
    setLoading(true);
    setError("");
    try {
      await updateSubscription(trimmed);
    } catch (e: any) {
      setError(e?.message ?? "Не удалось загрузить подписку. Проверьте ссылку.");
    } finally {
      setLoading(false);
    }
  };

  return (
    <motion.div
      className="flex flex-col h-full"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
    >
      {/* Top glow matching logo */}
      <div
        className="absolute top-0 left-1/2 -translate-x-1/2 pointer-events-none"
        style={{
          width: 320, height: 320,
          background: "radial-gradient(circle, rgba(37,99,235,0.18) 0%, transparent 70%)",
          borderRadius: "50%",
          top: -60,
        }}
      />

      <div className="flex flex-col items-center justify-center flex-1 px-6 relative">

        {/* Logo — 1.75× bigger, no border */}
        <motion.div
          initial={{ scale: 0.75, opacity: 0 }}
          animate={{ scale: 1, opacity: 1 }}
          transition={{ delay: 0.08, type: "spring", stiffness: 180, damping: 18 }}
          className="mb-5"
          style={{
            filter: "drop-shadow(0 0 28px rgba(37,99,235,0.5))",
          }}
        >
          <img
            src={logoUrl}
            alt="RustleBoost"
            style={{ width: 192, height: 192, objectFit: "contain" }}
            draggable={false}
          />
        </motion.div>

        <motion.div
          initial={{ y: 14, opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          transition={{ delay: 0.18 }}
          className="text-center mb-9"
        >
          <h1
            className="text-2xl font-bold text-white mb-2"
            style={{ letterSpacing: "-0.02em" }}
          >
            RustleBoost
          </h1>
          <p className="text-sm leading-relaxed" style={{ color: "rgba(255,255,255,0.38)" }}>
            Введите ключ подписки,<br />чтобы начать использование
          </p>
        </motion.div>

        {/* Input block */}
        <motion.div
          initial={{ y: 14, opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          transition={{ delay: 0.26 }}
          className="w-full"
        >
          <p
            className="text-xs font-semibold mb-2"
            style={{ color: "rgba(255,255,255,0.35)", letterSpacing: "0.06em", textTransform: "uppercase" }}
          >
            URL подписки
          </p>
          <input
            type="text"
            value={url}
            onChange={e => setUrl(e.target.value)}
            onKeyDown={e => e.key === "Enter" && handleSubmit()}
            placeholder="https://sub.lindavpn.com/token"
            autoFocus
            style={{
              width: "100%",
              padding: "13px 16px",
              fontSize: 14,
              borderRadius: 14,
              background: "rgba(37,99,235,0.06)",
              border: "1px solid rgba(37,99,235,0.22)",
              color: "white",
              outline: "none",
              WebkitUserSelect: "text",
              boxSizing: "border-box",
              transition: "border-color 0.2s",
            }}
          />

          {error && (
            <motion.div
              initial={{ opacity: 0, y: -4 }}
              animate={{ opacity: 1, y: 0 }}
              className="flex items-start gap-2 mt-3 text-xs"
              style={{ color: "#f87171" }}
            >
              <AlertCircle size={13} className="shrink-0 mt-0.5" />
              {error}
            </motion.div>
          )}

          <motion.button
            onClick={handleSubmit}
            disabled={loading || !url.trim()}
            whileTap={{ scale: 0.97 }}
            className="w-full mt-4 py-3.5 rounded-2xl font-semibold text-sm flex items-center justify-center gap-2 disabled:opacity-40"
            style={{
              background: "linear-gradient(135deg, #2563eb 0%, #1d4ed8 100%)",
              color: "white",
              border: "none",
              cursor: loading || !url.trim() ? "not-allowed" : "pointer",
              boxShadow: "0 6px 24px rgba(37,99,235,0.4)",
              transition: "opacity 0.2s, transform 0.1s",
            }}
          >
            {loading ? (
              <>
                <RefreshCw size={16} className="animate-spin" />
                Загрузка подписки...
              </>
            ) : (
              <>
                Подключить подписку
                <ArrowRight size={16} />
              </>
            )}
          </motion.button>
        </motion.div>
      </div>

      <p
        className="text-center pb-4"
        style={{ fontSize: 10, color: "rgba(255,255,255,0.13)" }}
      >
        RustleBoost · sing-box
      </p>
    </motion.div>
  );
}
