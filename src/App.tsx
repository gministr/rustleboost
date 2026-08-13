import { useEffect } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { AnimatePresence } from "framer-motion";
import { useVPNStore } from "./store/vpnStore";
import { useThemeStore } from "./store/themeStore";
import MainPage from "./pages/MainPage";
import SettingsPage from "./pages/SettingsPage";
import OnboardingPage from "./pages/OnboardingPage";
import TitleBar from "./components/TitleBar";
import NavBar from "./components/NavBar";
import UpdateBanner from "./components/UpdateBanner";

export default function App() {
  const { fetchStatus, fetchServers, fetchSettings, fetchSubscription, settings } = useVPNStore();
  const isDark = useThemeStore(s => s.isDark);

  useEffect(() => {
    fetchSettings();
    fetchServers();
    fetchStatus();
    fetchSubscription();

    const status = setInterval(fetchStatus, 2000);
    // Traffic and expiry only move when the daemon refreshes the
    // subscription, so a slow poll is enough to keep them current.
    const subscription = setInterval(fetchSubscription, 60_000);

    return () => {
      clearInterval(status);
      clearInterval(subscription);
    };
  }, []);

  const hasSubscription = !!settings.subscription_url;

  return (
    <div
      className="flex flex-col h-full rounded-2xl overflow-hidden"
      style={{ backgroundColor: "var(--c-bg)", transition: "background-color 0.3s" }}
    >
      {/* Ambient blobs */}
      <div className="fixed inset-0 pointer-events-none overflow-hidden">
        <div style={{
          position: "absolute", top: -160, left: "50%", transform: "translateX(-50%)",
          width: 320, height: 320, borderRadius: "50%", filter: "blur(80px)",
          background: isDark ? "rgba(37,99,235,0.12)" : "rgba(37,99,235,0.07)",
        }} />
        <div style={{
          position: "absolute", bottom: -128, right: -96,
          width: 288, height: 288, borderRadius: "50%", filter: "blur(80px)",
          background: isDark ? "rgba(29,78,216,0.15)" : "rgba(29,78,216,0.06)",
        }} />
        <div style={{
          position: "absolute", top: "50%", left: -128,
          width: 224, height: 224, borderRadius: "50%", filter: "blur(80px)",
          background: isDark ? "rgba(37,99,235,0.08)" : "rgba(37,99,235,0.04)",
        }} />
      </div>

      {/* TitleBar only shown after subscription is set */}
      {hasSubscription ? (
        <TitleBar />
      ) : (
        <OnboardingTitleBar />
      )}

      {hasSubscription && <UpdateBanner />}

      <div className="flex-1 overflow-hidden relative">
        <AnimatePresence mode="wait">
          {!hasSubscription ? (
            <OnboardingPage key="onboarding" />
          ) : (
            <Routes>
              <Route path="/" element={<MainPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          )}
        </AnimatePresence>
      </div>

      {hasSubscription && <NavBar />}
    </div>
  );
}

// Minimal title bar for onboarding - only window controls, no branding
function OnboardingTitleBar() {
  // Dynamic import to avoid issues
  const handleClose = async () => {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    getCurrentWindow().hide();
  };
  const handleMinimize = async () => {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    getCurrentWindow().minimize();
  };

  return (
    <div
      className="titlebar flex items-center justify-end px-3 shrink-0"
      style={{ height: 32 }}
    >
      <div className="flex items-center gap-1" style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}>
        <button
          onClick={handleMinimize}
          className="w-7 h-7 flex items-center justify-center rounded-lg hover:bg-white/10 text-white/30 hover:text-white/70 transition-colors"
        >
          <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
            <rect x="1" y="5.5" width="10" height="1" rx="0.5"/>
          </svg>
        </button>
        <button
          onClick={handleClose}
          className="w-7 h-7 flex items-center justify-center rounded-lg hover:bg-red-500/70 text-white/30 hover:text-white transition-colors"
        >
          <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
            <path d="M1.5 1.5L10.5 10.5M10.5 1.5L1.5 10.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
          </svg>
        </button>
      </div>
    </div>
  );
}
