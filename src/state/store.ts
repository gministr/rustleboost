import { create } from "zustand";
import { api, errorText, Server, Settings, Status, SubscriptionInfo, SupportInbox } from "../lib/api";
import { translate } from "../lib/strings";
import { syncTray } from "../lib/tray";

/** Состояние, которое видит человек — как VPNStatus в приложении iOS/Mac. */
export type UiStatus = "disconnected" | "connecting" | "connected" | "disconnecting" | "failed";

export type Page = "home" | "settings" | "support";
export type ThemePref = "system" | "light" | "dark";

interface AppState {
  ready: boolean;
  daemonDown: boolean;
  status: Status;
  uiStatus: UiStatus;
  connectedSince: number | null;
  servers: Server[];
  settings: Settings;
  info: SubscriptionInfo | null;
  selectedId: string;
  /** Нажали «Подключить» — до ответа службы кнопка уже показывает подключение. */
  pendingConnect: boolean;
  measuring: boolean;
  measuringIds: string[];
  refreshing: boolean;
  banner: string | null;
  page: Page;
  theme: ThemePref;
  mascot: boolean;
  inbox: SupportInbox;
  entryOpen: boolean;
  /** Что подставить в поле ключа при открытии. */
  entryPrefill: string;

  init: () => void;
  toggleConnection: () => Promise<void>;
  selectServer: (server: Server) => Promise<void>;
  setSubscription: (url: string) => Promise<void>;
  refreshSubscription: () => Promise<void>;
  removeSubscription: () => Promise<void>;
  measureAll: () => Promise<void>;
  measureOne: (id: string) => Promise<void>;
  saveSettings: (patch: Partial<Settings>) => Promise<void>;
  setTheme: (theme: ThemePref) => void;
  setMascot: (on: boolean) => void;
  setPage: (page: Page) => void;
  setEntryOpen: (open: boolean, prefill?: string) => void;
  refreshInbox: () => Promise<void>;
  dismissBanner: () => void;
}

const defaultSettings: Settings = {
  subscription_url: "",
  last_server_id: "",
  auto_connect: false,
  auto_update: true,
  update_interval: 12,
  dns_mode: "local",
  kill_switch: false,
  allow_lan: false,
  language: "ru",
  tun_mode: true,
  route_mode: "ru",
};

function readPref<T extends string>(key: string, fallback: T): T {
  try { return (localStorage.getItem(key) as T) || fallback; } catch { return fallback; }
}
function writePref(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* приватный режим */ }
}

function toUiStatus(status: Status, pending: boolean): UiStatus {
  if (pending && status.state === "disconnected") return "connecting";
  if (status.state === "disconnected" && status.error) return "failed";
  return status.state;
}

function applyTheme(pref: ThemePref) {
  const dark = pref === "dark" || (pref === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

let started = false;

export const useApp = create<AppState>((set, get) => {
  const lang = () => get().settings.language;

  const pullStatus = async () => {
    try {
      const status = await api.status();
      const prev = get();
      const uiStatus = toUiStatus(status, prev.pendingConnect);
      let since = prev.connectedSince;
      if (status.state === "connected") {
        const fromUptime = Date.now() - status.stats.uptime * 1000;
        // Таймер считается от момента подключения; обновляем, только если
        // разошлись больше чем на 2 с (новое подключение или пробуждение).
        if (since === null || Math.abs(since - fromUptime) > 2000) since = fromUptime;
      } else {
        since = null;
      }
      set({ status, uiStatus, connectedSince: since, ready: true, daemonDown: false });
      syncTray(uiStatus, status.server?.name);
    } catch {
      if (get().ready) set({ daemonDown: true });
    }
  };

  const pullServers = async () => {
    try {
      const servers = (await api.servers()) ?? [];
      const selected = get().selectedId;
      const keep = servers.some(s => s.id === selected);
      set({ servers, selectedId: keep ? selected : (servers[0]?.id ?? "") });
    } catch { /* служба ещё запускается */ }
  };

  const pullSettings = async () => {
    try {
      const settings = { ...defaultSettings, ...(await api.settings()) };
      set({ settings, selectedId: get().selectedId || settings.last_server_id });
    } catch { /* служба ещё запускается */ }
  };

  const pullSubscription = async () => {
    try {
      const sub = await api.subscription();
      set({ info: sub.info ?? null });
    } catch { /* нет подписки */ }
  };

  return {
    ready: false,
    daemonDown: false,
    status: { state: "disconnected", stats: { upload: 0, download: 0, uptime: 0 } },
    uiStatus: "disconnected",
    connectedSince: null,
    servers: [],
    settings: defaultSettings,
    info: null,
    selectedId: "",
    pendingConnect: false,
    measuring: false,
    measuringIds: [],
    refreshing: false,
    banner: null,
    page: "home",
    theme: readPref<ThemePref>("rb.theme", "system"),
    mascot: readPref<string>("rb.mascot", "on") !== "off",
    inbox: { unreadTickets: 0, unreadNotices: 0 },
    entryOpen: false,
    entryPrefill: "",

    init: () => {
      if (started) return;
      started = true;
      applyTheme(get().theme);
      window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => applyTheme(get().theme));
      (async () => {
        await pullSettings();
        await Promise.all([pullServers(), pullStatus(), pullSubscription()]);
        get().refreshInbox();
      })();
      setInterval(pullStatus, 1500);
      setInterval(pullSubscription, 60_000);
      setInterval(pullServers, 30_000);
      setInterval(() => get().refreshInbox(), 120_000);
    },

    toggleConnection: async () => {
      const { uiStatus, selectedId, servers, measuring } = get();
      if (measuring) return;
      if (uiStatus === "connected" || uiStatus === "connecting") {
        set({ pendingConnect: false, banner: null });
        try { await api.disconnect(); } catch (e) { set({ banner: errorText(e, translate(lang(), "error.daemon")) }); }
        await pullStatus();
        return;
      }
      if (uiStatus === "disconnecting") return;
      const id = selectedId || servers[0]?.id;
      if (!id) return;
      set({ pendingConnect: true, banner: null, uiStatus: "connecting" });
      syncTray("connecting");
      try {
        await api.connect(id);
      } catch (e) {
        set({ banner: errorText(e, translate(lang(), "error.connect")) });
      } finally {
        set({ pendingConnect: false });
        await pullStatus();
      }
    },

    selectServer: async (server: Server) => {
      set({ selectedId: server.id });
      const { uiStatus } = get();
      // Выбор сервера при включённом VPN сразу переключает соединение.
      if (uiStatus === "connected" || uiStatus === "connecting") {
        set({ pendingConnect: true, banner: null });
        try { await api.connect(server.id); }
        catch (e) { set({ banner: errorText(e, translate(lang(), "error.connect")) }); }
        finally { set({ pendingConnect: false }); await pullStatus(); }
      } else {
        get().saveSettings({ last_server_id: server.id });
      }
    },

    setSubscription: async (url: string) => {
      set({ refreshing: true, banner: null });
      try {
        const result = await api.updateSubscription(url.trim());
        set({ info: result.info ?? get().info });
        await Promise.all([pullSettings(), pullServers()]);
        set({ entryOpen: false });
      } catch (e) {
        throw new Error(errorText(e, translate(lang(), "error.subscription")));
      } finally {
        set({ refreshing: false });
      }
    },

    refreshSubscription: async () => {
      const url = get().settings.subscription_url;
      if (!url) return;
      set({ refreshing: true });
      try {
        const result = await api.updateSubscription(url);
        set({ info: result.info ?? get().info });
        await pullServers();
      } catch (e) {
        set({ banner: errorText(e, translate(lang(), "error.subscription")) });
      } finally {
        set({ refreshing: false });
      }
    },

    removeSubscription: async () => {
      if (get().uiStatus === "connected") { try { await api.disconnect(); } catch { /* */ } }
      await get().saveSettings({ subscription_url: "", last_server_id: "" });
      set({ servers: [], info: null, selectedId: "", page: "home" });
    },

    measureAll: async () => {
      if (get().measuring) return;
      set({ measuring: true });
      try {
        await api.pingAll();
        // Замер идёт в службе несколько секунд: результаты подтягиваются
        // по мере готовности.
        for (let i = 0; i < 8; i++) {
          await new Promise(r => setTimeout(r, 1500));
          await pullServers();
        }
      } catch { /* */ } finally {
        set({ measuring: false });
      }
    },

    measureOne: async (id: string) => {
      if (get().measuringIds.includes(id)) return;
      set({ measuringIds: [...get().measuringIds, id] });
      try {
        const { latency } = await api.ping(id);
        set({ servers: get().servers.map(s => (s.id === id ? { ...s, latency } : s)) });
      } catch { /* */ } finally {
        set({ measuringIds: get().measuringIds.filter(x => x !== id) });
      }
    },

    saveSettings: async (patch: Partial<Settings>) => {
      const merged = { ...get().settings, ...patch } as Settings;
      set({ settings: merged });
      try {
        const saved = await api.saveSettings(merged);
        set({ settings: { ...defaultSettings, ...saved } });
        if (patch.auto_connect !== undefined) await api.setAutostart(!!patch.auto_connect).catch(() => {});
      } catch (e) {
        set({ banner: errorText(e, translate(lang(), "error.daemon")) });
      }
    },

    setTheme: (theme: ThemePref) => { writePref("rb.theme", theme); applyTheme(theme); set({ theme }); },
    setMascot: (on: boolean) => { writePref("rb.mascot", on ? "on" : "off"); set({ mascot: on }); },
    setPage: (page: Page) => set({ page }),
    setEntryOpen: (open: boolean, prefill = "") => set({ entryOpen: open, entryPrefill: prefill }),

    refreshInbox: async () => {
      if (!get().settings.subscription_url) return;
      try { set({ inbox: await api.inbox() }); } catch { /* сервер поддержки недоступен */ }
    },

    dismissBanner: () => set({ banner: null }),
  };
});

/** Серверы вкладки каталога. */
export function serversOf(servers: Server[], group: "regular" | "rustleboost" | "warp"): Server[] {
  return servers.filter(s => (s.group || "regular") === group);
}
