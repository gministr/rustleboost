import { useState, useEffect } from "react";
import { check } from "@tauri-apps/plugin-updater";
import { relaunch } from "@tauri-apps/plugin-process";
import { motion, AnimatePresence } from "framer-motion";
import { Download, X } from "lucide-react";
import { useT } from "../i18n";

export default function UpdateBanner() {
  const [update, setUpdate] = useState<{ version: string; body: string | null | undefined } | null>(null);
  const [status, setStatus] = useState<"idle" | "downloading" | "done">("idle");
  const [dismissed, setDismissed] = useState(false);
  const t = useT();

  useEffect(() => {
    const look = () => {
      check()
        .then(u => { if (u?.available) setUpdate({ version: u.version, body: u.body }); })
        .catch(() => {}); // no internet, or no release published yet
    };

    look();
    // The app often stays open for days, so a start-up-only check would let a
    // release sit unnoticed until the next restart.
    const timer = setInterval(look, 6 * 60 * 60 * 1000);
    return () => clearInterval(timer);
  }, []);

  const handleUpdate = async () => {
    if (!update) return;
    setStatus("downloading");
    try {
      const u = await check();
      if (!u?.available) {
        setStatus("idle");
        return;
      }
      await u.downloadAndInstall();
      setStatus("done");
      setTimeout(() => relaunch(), 1500);
    } catch {
      setStatus("idle");
    }
  };

  if (!update || dismissed) return null;

  return (
    <AnimatePresence>
      <motion.div
        initial={{ opacity: 0, y: -8 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: -8 }}
        style={{
          margin: "8px 12px 0",
          padding: "10px 14px",
          borderRadius: 12, flexShrink: 0,
          background: "rgba(37,99,235,0.12)",
          border: "1px solid rgba(37,99,235,0.35)",
          display: "flex", alignItems: "center", gap: 10,
        }}
      >
        <Download size={14} style={{ color: "#60a5fa", flexShrink: 0 }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <p style={{ fontSize: 12, fontWeight: 600, color: "#93c5fd", margin: 0 }}>
            {t("updateAvailable")} v{update.version}
          </p>
          {update.body && (
            <p style={{ fontSize: 11, color: "rgba(147,197,253,0.7)", margin: "2px 0 0",
              overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {update.body}
            </p>
          )}
        </div>
        <button
          onClick={handleUpdate}
          disabled={status !== "idle"}
          style={{
            padding: "5px 12px", borderRadius: 8, fontSize: 12, fontWeight: 600,
            background: status === "done" ? "#4ade80" : "#2563eb",
            color: "white", border: "none", cursor: status === "idle" ? "pointer" : "default",
            flexShrink: 0, transition: "background 0.2s",
          }}
        >
          {status === "downloading" ? t("updateDownloading") : status === "done" ? t("updateDone") : t("updateAction")}
        </button>
        {status === "idle" && (
          <button onClick={() => setDismissed(true)}
            style={{ background: "none", border: "none", cursor: "pointer",
              color: "rgba(147,197,253,0.5)", flexShrink: 0, padding: 2 }}>
            <X size={12} />
          </button>
        )}
      </motion.div>
    </AnimatePresence>
  );
}
