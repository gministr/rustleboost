import { create } from "zustand";

interface ThemeStore {
  isDark: boolean;
  toggle: () => void;
}

function applyTheme(dark: boolean) {
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

const saved = typeof localStorage !== "undefined" ? localStorage.getItem("rb-theme") : null;
const initialDark = saved !== "light";
applyTheme(initialDark);

export const useThemeStore = create<ThemeStore>((set) => ({
  isDark: initialDark,
  toggle: () =>
    set((s) => {
      const next = !s.isDark;
      localStorage.setItem("rb-theme", next ? "dark" : "light");
      applyTheme(next);
      return { isDark: next };
    }),
}));
