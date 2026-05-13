import { invoke } from "@tauri-apps/api/core";

export interface Server {
  id: string;
  name: string;
  country: string;
  flag: string;
  protocol: string;
  address: string;
  port: number;
  latency: number;
  raw_uri: string;
}

export interface ConnectionStats {
  upload: number;
  download: number;
  uptime: number;
}

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "disconnecting";

export interface Status {
  state: ConnectionState;
  server?: Server;
  stats: ConnectionStats;
  error?: string;
}

export interface Settings {
  subscription_url: string;
  last_server_id: string;
  auto_connect: boolean;
  auto_update: boolean;
  update_interval: number;
  dns_mode: "system" | "local";
  kill_switch: boolean;
  allow_lan: boolean;
  language: "ru" | "en";
  tun_mode: boolean;
  route_mode: "all" | "ru" | "cn";
}

async function call<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  return invoke<T>(cmd, args);
}

export interface HWIDInfo {
  hwid: string;
  os: string;
  ver: string;
  model: string;
}

export const api = {
  getStatus: () => call<Status>("get_status"),
  getHWID: () => call<HWIDInfo>("get_hwid"),
  connect: (serverId: string) => call<{ status: string }>("connect_server", { serverId }),
  disconnect: () => call<{ status: string }>("disconnect"),
  getServers: () => call<Server[]>("get_servers"),
  updateSubscription: (url: string) =>
    call<{ status: string; servers: number }>("update_subscription", { url }),
  getSettings: () => call<Settings>("get_settings"),
  saveSettings: (settings: Partial<Settings>) =>
    call<Settings>("save_settings", { settings }),
  pingServer: (serverId: string) =>
    call<{ latency: number }>("ping_server", { serverId }),
  pingAll: () => call<{ status: string }>("ping_all"),
  setAutostart: (enabled: boolean) =>
    call<void>("set_autostart", { enabled }),
};

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

export function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

export function latencyColor(ms: number): string {
  if (ms < 0) return "text-gray-500";
  if (ms < 100) return "text-green-400";
  if (ms < 200) return "text-yellow-400";
  return "text-red-400";
}

export function latencyLabel(ms: number): string {
  if (ms < 0) return "—";
  return `${ms}ms`;
}
