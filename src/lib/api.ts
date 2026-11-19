import { invoke } from "@tauri-apps/api/core";

// Связь со службой RustleBoost (daemon). Интерфейс обращается к ней прямо по
// http://localhost:<порт>; порт выбирает и сообщает оболочка Tauri.

export interface Server {
  id: string;
  name: string;
  country: string;
  flag: string;
  protocol: string;
  transport: string;
  security: string;
  engine: string;
  address: string;
  port: number;
  latency: number;
  raw_uri: string;
  /** regular | rustleboost | warp — вкладка каталога */
  group?: string;
}

export interface SubscriptionInfo {
  upload: number;
  download: number;
  /** 0 — без лимита */
  total: number;
  /** unix-секунды; 0 — бессрочно */
  expire: number;
  title: string;
  announce: string;
  support_url: string;
  web_page_url: string;
  updated_at: number;
}

export type ConnectionState = "disconnected" | "connecting" | "connected" | "disconnecting";

export interface Status {
  state: ConnectionState;
  server?: Server;
  stats: { upload: number; download: number; uptime: number };
  engine?: string;
  error?: string;
  warning?: string;
}

export interface Settings {
  subscription_url: string;
  last_server_id: string;
  auto_connect: boolean;
  auto_update: boolean;
  update_interval: number;
  dns_mode: string;
  kill_switch: boolean;
  allow_lan: boolean;
  language: "ru" | "en";
  tun_mode: boolean;
  route_mode: "all" | "ru" | "cn";
  [key: string]: unknown;
}

export interface HWIDInfo { hwid: string; os: string; ver: string; model: string; }

export interface Versions { app: string; xray: string; hysteria2: string; }

export interface SupportMessage { id: number; author: "user" | "admin" | string; body: string; createdAt: string; }
export interface SupportTicket {
  id: number;
  status: "open" | "closed" | string;
  createdAt: string;
  updatedAt: string;
  closedAt?: string;
  unread: boolean;
  preview: string;
  messages?: SupportMessage[];
}
export interface SupportNotice { id: number; body: string; createdAt: string; read: boolean; }
export interface SupportInbox { unreadTickets: number; unreadNotices: number; }

let portPromise: Promise<number> | null = null;

function daemonPort(): Promise<number> {
  if (!portPromise) {
    portPromise = invoke<number>("get_daemon_port").catch(() => 7777);
  }
  return portPromise;
}

/** Ошибка, понятная человеку: служба отвечает текстом в поле error. */
export class DaemonError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(method: string, path: string, body?: unknown, timeoutMs = 95_000): Promise<T> {
  const port = await daemonPort();
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  let resp: Response;
  try {
    resp = await fetch(`http://localhost:${port}${path}`, {
      method,
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    });
  } catch {
    throw new DaemonError("Служба RustleBoost не отвечает — перезапустите приложение", 0);
  } finally {
    clearTimeout(timer);
  }
  const text = await resp.text();
  let data: any = undefined;
  try { data = text ? JSON.parse(text) : undefined; } catch { data = undefined; }
  if (!resp.ok) {
    const message = (data && typeof data.error === "string" && data.error) || text || `ошибка ${resp.status}`;
    throw new DaemonError(message, resp.status);
  }
  return data as T;
}

export const api = {
  status: () => request<Status>("GET", "/api/status", undefined, 8000),
  servers: () => request<Server[]>("GET", "/api/servers", undefined, 8000),
  subscription: () => request<{ url: string; servers: number; info: SubscriptionInfo }>("GET", "/api/subscription", undefined, 8000),
  updateSubscription: (url: string) =>
    request<{ status: string; servers: number; info: SubscriptionInfo }>("POST", "/api/subscription", { url }, 60_000),
  connect: (serverId: string) => request<{ status: string }>("POST", "/api/connect", { server_id: serverId }),
  disconnect: () => request<{ status: string }>("POST", "/api/disconnect", {}),
  settings: () => request<Settings>("GET", "/api/settings", undefined, 8000),
  saveSettings: (settings: Settings) => request<Settings>("POST", "/api/settings", settings, 15_000),
  ping: (serverId: string) => request<{ latency: number }>("POST", "/api/ping", { server_id: serverId }, 30_000),
  pingAll: () => request<{ status: string }>("POST", "/api/ping-all", {}, 15_000),
  hwid: () => request<HWIDInfo>("GET", "/api/hwid", undefined, 8000),
  versions: () => request<Versions>("GET", "/api/version", undefined, 8000),

  inbox: () => request<SupportInbox>("GET", "/api/support/inbox", undefined, 25_000),
  tickets: () => request<SupportTicket[]>("GET", "/api/support/tickets", undefined, 25_000),
  ticket: (id: number) => request<SupportTicket>("GET", `/api/support/ticket?id=${id}`, undefined, 25_000),
  createTicket: (body: string) => request<SupportTicket>("POST", "/api/support/tickets", { body }, 25_000),
  reply: (id: number, body: string) => request<SupportTicket>("POST", `/api/support/ticket?id=${id}`, { body }, 25_000),
  notices: () => request<SupportNotice[]>("GET", "/api/support/notices", undefined, 25_000),
  markNoticesRead: () => request<{ ok: boolean }>("POST", "/api/support/notices", {}, 25_000),
  deleteNotices: (id?: number) => request<{ ok: boolean }>("DELETE", `/api/support/notices${id ? `?id=${id}` : ""}`, undefined, 25_000),

  setAutostart: (enabled: boolean) => invoke<void>("set_autostart", { enabled }),
};

export function errorText(e: unknown, fallback: string): string {
  if (e instanceof Error && e.message) return e.message;
  if (typeof e === "string" && e.trim()) return e;
  return fallback;
}
