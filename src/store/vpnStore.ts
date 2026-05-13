import { create } from "zustand";
import { api, Status, Server, Settings } from "../api/daemon";

interface VPNStore {
  status: Status;
  servers: Server[];
  settings: Settings;
  daemonReady: boolean;
  loading: boolean;
  connectingId: string | null;
  error: string | null;

  fetchStatus: () => Promise<void>;
  fetchServers: () => Promise<void>;
  fetchSettings: () => Promise<void>;
  connect: (serverId: string) => Promise<void>;
  disconnect: () => Promise<void>;
  updateSubscription: (url: string) => Promise<void>;
  saveSettings: (s: Partial<Settings>) => Promise<void>;
  pingAll: () => Promise<void>;
  clearError: () => void;
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
  route_mode: "all",
};

export const useVPNStore = create<VPNStore>((set, get) => ({
  status: defaultStatus,
  servers: [],
  settings: defaultSettings,
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

  connect: async (serverId: string) => {
    set({ connectingId: serverId, error: null });
    try {
      await api.connect(serverId);
      const status = await api.getStatus();
      set({ status, connectingId: null });
    } catch (e: any) {
      set({
        connectingId: null,
        error: e?.message ?? "Connection failed",
        status: { ...get().status, state: "disconnected" },
      });
    }
  },

  disconnect: async () => {
    set({ error: null });
    try {
      await api.disconnect();
      set({ status: defaultStatus });
    } catch (e: any) {
      set({ error: e?.message ?? "Disconnect failed" });
    }
  },

  updateSubscription: async (url: string) => {
    set({ loading: true, error: null });
    try {
      await api.updateSubscription(url);
      const [servers, settings] = await Promise.all([
        api.getServers(),
        api.getSettings(),
      ]);
      set({ servers: servers ?? [], settings, loading: false });
    } catch (e: any) {
      set({ loading: false, error: e?.message ?? "Failed to update subscription" });
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
    } catch (e: any) {
      set({ error: e?.message ?? "Failed to save settings" });
    }
  },

  pingAll: async () => {
    try {
      await api.pingAll();
      setTimeout(async () => {
        const servers = await api.getServers();
        set({ servers: servers ?? [] });
      }, 3000);
    } catch {
      //
    }
  },

  clearError: () => set({ error: null }),
}));
