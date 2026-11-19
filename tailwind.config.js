/** @type {import('tailwindcss').Config} */
// Цвета — переменные из src/index.css: та же палитра, что у приложения
// iOS/Mac (RustleBoost/DesignSystem/BrandColors.swift), светлая и тёмная.
export default {
  content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
  theme: {
    extend: {
      colors: {
        accent: "var(--accent)",
        connected: "var(--connected)",
        pending: "var(--pending)",
        danger: "var(--danger)",
        bg: "var(--bg)",
        surface: "var(--surface)",
        elevated: "var(--surface-elevated)",
        separator: "var(--separator)",
        primary: "var(--text-primary)",
        secondary: "var(--text-secondary)",
      },
      borderRadius: { s: "10px", m: "16px", l: "24px" },
      fontFamily: { sans: ["Nunito", "Segoe UI Variable", "Segoe UI", "system-ui", "sans-serif"] },
    },
  },
  plugins: [],
};
