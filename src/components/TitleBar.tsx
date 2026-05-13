import { X, Minus, Sun, Moon } from "lucide-react";
import { getCurrentWindow } from "@tauri-apps/api/window";
import logoDarkUrl from "../assets/logo-dark.png";
import logoLightUrl from "../assets/logo-light.png";
import { useThemeStore } from "../store/themeStore";

export default function TitleBar() {
  const win = getCurrentWindow();
  const { isDark, toggle } = useThemeStore();

  // Dark theme → show light (silver) logo; light theme → show dark logo
  const logoSrc = isDark ? logoLightUrl : logoDarkUrl;

  return (
    <div className="titlebar flex items-center justify-between px-4 select-none shrink-0">
      <div className="flex items-center gap-2">
        <img
          src={logoSrc}
          alt="RustleBoost"
          style={{
            width: 24,
            height: 24,
            objectFit: "contain",
            // multiply removes white halos: white × bg = bg (transparent)
            // screen removes dark halos for the light logo on dark bg
            mixBlendMode: isDark ? "screen" : "multiply",
            filter: isDark
              ? "drop-shadow(0 0 6px rgba(37,99,235,0.7))"
              : "drop-shadow(0 0 4px rgba(37,99,235,0.5))",
            transition: "filter 0.3s",
          }}
          draggable={false}
        />
        <span
          className="text-sm font-semibold"
          style={{ color: "var(--c-text)", letterSpacing: "-0.01em", transition: "color 0.3s" }}
        >
          RustleBoost
        </span>
      </div>

      <div
        className="flex items-center gap-1"
        style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
      >
        {/* Theme toggle */}
        <button
          onClick={toggle}
          className="w-7 h-7 flex items-center justify-center rounded-lg transition-colors"
          style={{ color: "var(--c-text-sub)" }}
          onMouseEnter={e => (e.currentTarget.style.background = "var(--c-surface-hover)")}
          onMouseLeave={e => (e.currentTarget.style.background = "transparent")}
          title={isDark ? "Светлая тема" : "Тёмная тема"}
        >
          {isDark ? <Sun size={13} /> : <Moon size={13} />}
        </button>

        <button
          onClick={() => win.minimize()}
          className="w-7 h-7 flex items-center justify-center rounded-lg transition-colors"
          style={{ color: "var(--c-text-sub)" }}
          onMouseEnter={e => (e.currentTarget.style.background = "var(--c-surface-hover)")}
          onMouseLeave={e => (e.currentTarget.style.background = "transparent")}
        >
          <Minus size={12} />
        </button>
        <button
          onClick={() => win.hide()}
          className="w-7 h-7 flex items-center justify-center rounded-lg transition-colors"
          style={{ color: "var(--c-text-sub)" }}
          onMouseEnter={e => {
            e.currentTarget.style.background = "rgba(239,68,68,0.75)";
            e.currentTarget.style.color = "white";
          }}
          onMouseLeave={e => {
            e.currentTarget.style.background = "transparent";
            e.currentTarget.style.color = "var(--c-text-sub)";
          }}
        >
          <X size={12} />
        </button>
      </div>
    </div>
  );
}
