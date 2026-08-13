import { create } from "zustand";
import { api, Status, Server, Settings, SubscriptionInfo } from "../api/daemon";
import { translate, Language, TranslationKey } from "../i18n";

interface VPNStore {
  status: Status;
  servers: Server[];
  settings: Settings;
  info: SubscriptionInfo | null;
  daemonReady: boolean;
  loading: boolean;
  connectingId: string | null;
  error: string | null;

  fetchStatus: () => Promise<void>;
  fetchServers: () => Promise<void>;
  fetchSettings: () => Promise<void>;
  fetchSubscription: () => Promise<void>;
  connect: (serverId: string) => Promise<void>;
  disconnect: () => Promise<void>;
  updateSubscription: (url: string) => Promise<void>;
  saveSettings: (s: Partial<Settings>) => Promise<void>;
  pingAll: () => Promise<void>;
  clearError: () => void;
}

/**
 * Tauri rejects commands with a plain string; Error instances carry .message.
 * The daemon already localises what it reports, so its text is preferred and
 * the translated key only fills in when nothing came back.
 */
function errorText(e: unknown, language: Language, key: TranslationKey): string {
  if (typeof e === "string" && e.trim()) return e;
  if (e instanceof Error && e.message) return e.message;
  return translate(language, key);
}

const defaultStatus: Status = {
  state: "disconnected",
  stats: { upload: 0, download: 0, uptime: 0 },
};

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

export const useVPNStore = create<VPNStore>((set, get) => ({
  status: defaultStatus,
  servers: [],
  settings: defaultSettings,
  info: null,
  daemonReady: false,
  loading: false,
  connectingId: null,
  error: null,

  fetchStatus: async () => {
    try {
      const status = await api.getStatus();
      set({ status, daemonReady: true });
    } catch {
      // daemon not ready yet
    }
  },

  fetchServers: async () => {
    try {
      const servers = await api.getServers();
      set({ servers: servers ?? [] });
    } catch {
      //
    }
  },

  fetchSettings: async () => {
    try {
      const settings = await api.getSettings();
      set({ settings });
    } catch {
      // keep defaults while daemon starts
    }
  },

  fetchSubscription: async () => {
    try {
      const sub = await api.getSubscription();
      set({ info: sub.info ?? null });
    } catch {
      // daemon not ready yet
    }
  },

  connect: async (serverId: string) => {
    set({ connectingId: serverId, error: null });
    try {
      await api.connect(serverId);
      const status = await api.getStatus();
      set({ status, connectingId: null });
    } catch (e: unknown) {
      set({
        connectingId: null,
        error: errorText(e, get().settings.language as Language, "errConnect"),
        status: { ...get().status, state: "disconnected" },
      });
    }
  },

  disconnect: async () => {
    set({ error: null });
    try {
      await api.disconnect();
      set({ status: defaultStatus });
    } catch (e: unknown) {
      set({ error: errorText(e, get().settings.language as Language, "errDisconnect") });
    }
  },

  updateSubscription: async (url: string) => {
    set({ loading: true, error: null });
    try {
      const result = await api.updateSubscription(url);
      const [servers, settings] = await Promise.all([
        api.getServers(),
        api.getSettings(),
      ]);
      set({
        servers: servers ?? [],
        settings,
        info: result.info ?? get().info,
        loading: false,
      });
    } catch (e: unknown) {
      set({ loading: false, error: errorText(e, get().settings.language as Language, "errSubscription") });
      throw e;
    }
  },

  saveSettings: async (incoming: Partial<Settings>) => {
    try {
      const merged = { ...get().settings, ...incoming };
      const settings = await api.saveSettings(merged);
      set({ settings });
      if (incoming.auto_connect !== undefined) {
        await api.setAutostart(incoming.auto_connect).catch(() => {});
      }
    } catch (e: unknown) {
      set({ error: errorText(e, get().settings.language as Language, "errSettings") });
    }
  },

  // Measuring runs in the daemon and takes a few seconds — it opens a real
  // request through every node. Poll so results appear as they land instead
  // of after one arbitrary delay.
  pingAll: async () => {
    try {
      await api.pingAll();
    } catch {
      return;
    }

    for (let i = 0; i < 10; i++) {
      await new Promise(resolve => setTimeout(resolve, 1500));
      try {
        const servers = await api.getServers();
        set({ servers: servers ?? [] });
      } catch {
        // daemon busy; try again on the next tick
      }
    }
  },

  clearError: () => set({ error: null }),
}));
