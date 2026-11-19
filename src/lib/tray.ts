import { TrayIcon } from "@tauri-apps/api/tray";
import idleUrl from "../assets/tray/tray-idle.png";
import connectedUrl from "../assets/tray/tray-connected.png";
import type { UiStatus } from "../state/store";

// Значок в трее — как собака в строке меню Mac: белая, а при подключении
// зелёная. Трей создаёт оболочка (src-tauri/src/tray.rs, id "main").

const cache = new Map<string, Uint8Array>();
let last = "";

async function bytes(url: string): Promise<Uint8Array> {
  const hit = cache.get(url);
  if (hit) return hit;
  const data = new Uint8Array(await (await fetch(url)).arrayBuffer());
  cache.set(url, data);
  return data;
}

export async function syncTray(status: UiStatus, server?: string) {
  const connected = status === "connected";
  const tooltip = connected ? `RustleBoost — ${server ?? "подключено"}` : "RustleBoost";
  const key = `${connected}|${tooltip}`;
  if (key === last) return;
  last = key;
  try {
    const tray = await TrayIcon.getById("main");
    if (!tray) return;
    await tray.setIcon(await bytes(connected ? connectedUrl : idleUrl));
    await tray.setTooltip(tooltip);
  } catch {
    last = "";
  }
}
