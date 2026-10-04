import { invoke } from "@tauri-apps/api/core";

export interface Server {
  id: string;
  name: string;
  country: string;
  flag: string;
  protocol: string;
  /** tcp | grpc | xhttp | ws | httpupgrade | kcp — decides which core carries it */
  transport: string;
  /** reality | tls | none */
  security: string;
  /** "xray" | "singbox" */
  engine: string;
  address: string;
  port: number;
  latency: number;
  raw_uri: string;
}

/** Traffic and validity, as reported by the subscription's own headers. */
export interface SubscriptionInfo {
  upload: number;
  download: number;
  /** 0 means unlimited */
  total: number;
  /** unix seconds; 0 means no expiry */
  expire: number;
  title: string;
  announce: string;
  support_url: string;
  web_page_url: string;
  updated_at: number;
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
  /** which cores are carrying the session, e.g. "sing-box + xray" */
  engine?: string;
  error?: string;
  /** set when "connected" could not be verified — see UpdateBanner-style hint in MainPage */
  warning?: string;
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
  /**
   * Which core carries proxy traffic. Which implementation's handshake gets
   * through a given network isn't predictable from the app's side — it can
   * differ between two people on the same subscription — so this is a user
   * choice, not something decided automatically.
   */
  router_mode: "auto" | "singbox" | "xray";
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
  getSubscription: () =>
    call<{ url: string; servers: number; info: SubscriptionInfo }>("get_subscription"),
  updateSubscription: (url: string) =>
    call<{ status: string; servers: number; info: SubscriptionInfo }>("update_subscription", { url }),
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

/** Short label for a node's transport, e.g. "GRPC · REALITY". */
export function transportLabel(server: Server): string {
  const parts = [server.transport, server.security]
    .filter(p => p && p !== "none")
    .map(p => p.toUpperCase());
  return parts.join(" · ");
}

/** Callers handle expire === 0 (no expiry) themselves, so it can be localised. */
export function formatExpiry(expire: number): string {
  if (!expire) return "";
  return new Date(expire * 1000).toLocaleDateString("ru-RU", {
    day: "2-digit", month: "2-digit", year: "numeric",
  });
}

export function daysLeft(expire: number): number | null {
  if (!expire) return null;
  return Math.ceil((expire * 1000 - Date.now()) / 86_400_000);
}

export function latencyColor(ms: number): string {
  if (ms < 0) return "text-gray-500";
  if (ms < 250) return "text-green-400";
  if (ms < 500) return "text-yellow-400";
  return "text-red-400";
}

export function latencyLabel(ms: number): string {
  if (ms < 0) return "—";
  return `${ms}ms`;
}
