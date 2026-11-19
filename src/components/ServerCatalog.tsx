import { useEffect, useMemo, useState } from "react";
import { ChevronDown, Package, RotateCw, Timer } from "lucide-react";
import Flag, { displayName, flagCode } from "./Flag";
import { Card, IconButton, Segmented, Spinner } from "./ui";
import { serversOf, useApp } from "../state/store";
import { useT } from "../lib/strings";
import type { Server, SubscriptionInfo } from "../lib/api";

// Каталог серверов — как ServerCatalogSection в приложении iOS/Mac:
// вкладки «Обычные / RustleBoost / WARP», группа подписки с трафиком и
// сроком, строки серверов с флагом, цепочкой протокола и задержкой.

type Tab = "regular" | "rustleboost" | "warp";

// Замеры, которые уже делались: −1 от службы значит и «не замерено», и
// «нет ответа» — различаем сами, как LatencyLabel на iOS.
const measured = new Set<string>();

function protocolChain(s: Server): string {
  return [s.protocol, s.transport, s.security]
    .filter(p => p && p !== "none" && p !== "udp")
    .map(p => p.toUpperCase())
    .join(" · ");
}

function formatBytes(bytes: number): string {
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(gb >= 100 ? 0 : 2)} ГБ`;
  return `${(bytes / 1024 ** 2).toFixed(0)} МБ`;
}

function OnlineBadge({ label }: { label: string }) {
  return (
    <span style={{ display: "flex", alignItems: "center", gap: 5, fontSize: 12, fontWeight: 700, color: "var(--text-secondary)" }}>
      <span style={{ width: 8, height: 8, borderRadius: 4, background: "#34c759" }} />
      {label}
    </span>
  );
}

function LatencyLabel({ server, busy }: { server: Server; busy: boolean }) {
  const t = useT();
  if (busy) return <Spinner size={13} />;
  if (server.latency > 0) {
    return <span className="metric" style={{ fontSize: 12, fontWeight: 700, color: "var(--text-primary)" }}>{server.latency} ms</span>;
  }
  if (measured.has(server.id)) {
    return <span style={{ fontSize: 12, fontWeight: 700, color: "var(--text-secondary)" }}>{t("latency.noAnswer")}</span>;
  }
  return <span style={{ fontSize: 12, color: "var(--text-secondary)", opacity: 0.6 }}>—</span>;
}

function ServerRow({ server, online }: { server: Server; online: boolean }) {
  const t = useT();
  const selected = useApp(s => s.selectedId === server.id);
  const select = useApp(s => s.selectServer);
  const measureOne = useApp(s => s.measureOne);
  const busy = useApp(s => s.measuringIds.includes(server.id) || (s.measuring && !online));
  const connected = useApp(s => s.uiStatus === "connected");
  return (
    <button
      onClick={() => select(server)}
      className="server-row"
      style={{
        width: "100%", display: "flex", alignItems: "center", gap: 14, padding: "13px 16px", textAlign: "left",
        position: "relative", background: selected ? "color-mix(in srgb, var(--accent) 12%, transparent)" : undefined,
      }}
    >
      {selected && <span style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: 3, background: "var(--accent)" }} />}
      <Flag code={flagCode(server)} size={40} />
      <span style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 3 }}>
        <span style={{ fontSize: 14, fontWeight: 700, color: "var(--text-primary)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {displayName(server.name)}
        </span>
        <span style={{ fontSize: 10, fontWeight: 600, color: "var(--text-secondary)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {protocolChain(server)}
        </span>
      </span>
      {online ? (
        <OnlineBadge label="онлайн" />
      ) : (
        <>
          <LatencyLabel server={server} busy={busy} />
          <IconButton
            title={t("latency.measureOne")}
            disabled={connected || busy}
            onClick={() => { measured.add(server.id); measureOne(server.id); }}
            size={32}
          >
            <Timer size={15} strokeWidth={2.4} />
          </IconButton>
        </>
      )}
    </button>
  );
}

function GroupHeader({ title, count, info, expanded, onToggle, online }: {
  title: string; count: number; info?: SubscriptionInfo | null; expanded: boolean; onToggle: () => void; online: boolean;
}) {
  const t = useT();
  const measuring = useApp(s => s.measuring);
  const refreshing = useApp(s => s.refreshing);
  const connected = useApp(s => s.uiStatus === "connected");
  const measureAll = useApp(s => s.measureAll);
  const refresh = useApp(s => s.refreshSubscription);
  const markAll = () => useApp.getState().servers.forEach(s => measured.add(s.id));

  const used = info ? info.upload + info.download : 0;
  const fraction = info && info.total > 0 ? Math.min(1, used / info.total) : 0;
  const expiry = info?.expire
    ? new Date(info.expire * 1000).toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit", year: "numeric" })
    : null;

  return (
    <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Package size={14} strokeWidth={2.6} style={{ color: "var(--accent)", flexShrink: 0 }} />
        <span style={{ fontSize: 14, fontWeight: 800, color: "var(--text-primary)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{title}</span>
        <span style={{ fontSize: 12, fontWeight: 700, color: "var(--text-secondary)" }}>{count}</span>
        <span style={{ flex: 1 }} />
        {!online && (
          <IconButton title={t("latency.measureSubscription")} disabled={connected} busy={measuring} onClick={() => { markAll(); measureAll(); }}>
            <Timer size={14} strokeWidth={2.6} />
          </IconButton>
        )}
        <IconButton title={t("subscription.refreshOne")} busy={refreshing} onClick={refresh}>
          <RotateCw size={14} strokeWidth={2.6} />
        </IconButton>
        <IconButton title="" onClick={onToggle} color="var(--text-secondary)">
          <ChevronDown size={14} strokeWidth={2.6} style={{ transform: expanded ? "none" : "rotate(-90deg)", transition: "transform 0.2s" }} />
        </IconButton>
      </div>
      {info && (info.total > 0 || info.expire > 0 || used > 0) && (
        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <div style={{ height: 4, borderRadius: 2, background: "var(--surface-elevated)", overflow: "hidden" }}>
            <div style={{ width: `${fraction * 100}%`, height: "100%", background: "var(--accent)" }} />
          </div>
          <div style={{ display: "flex", justifyContent: "space-between", fontSize: 11, color: "var(--text-secondary)" }}>
            <span>{formatBytes(used)} / {info.total > 0 ? formatBytes(info.total) : "∞"}</span>
            <span>{expiry ? `${t("subscription.until")} ${expiry}` : t("subscription.forever")}</span>
          </div>
        </div>
      )}
      {info?.announce && (
        <div className="selectable" style={{
          fontSize: 11, color: "var(--text-secondary)", padding: 8, borderRadius: 10, lineHeight: 1.4, whiteSpace: "pre-wrap",
          background: "color-mix(in srgb, var(--accent) 8%, transparent)",
        }}>{info.announce}</div>
      )}
    </div>
  );
}

function Group({ id, title, servers, info, online, collapsed, onToggle }: {
  id: string; title: string; servers: Server[]; info?: SubscriptionInfo | null; online: boolean; collapsed: boolean; onToggle: (id: string) => void;
}) {
  return (
    <Card style={{ overflow: "hidden" }}>
      <GroupHeader title={title} count={servers.length} info={info} expanded={!collapsed} onToggle={() => onToggle(id)} online={online} />
      {!collapsed && servers.map(s => (
        <div key={s.id} style={{ borderTop: "1px solid var(--separator)" }}>
          <ServerRow server={s} online={online} />
        </div>
      ))}
    </Card>
  );
}

export default function ServerCatalog() {
  const t = useT();
  const servers = useApp(s => s.servers);
  const info = useApp(s => s.info);
  const selectedId = useApp(s => s.selectedId);
  const autoSelecting = false;

  const regular = useMemo(() => serversOf(servers, "regular"), [servers]);
  const rustleboost = useMemo(() => serversOf(servers, "rustleboost"), [servers]);
  const warp = useMemo(() => serversOf(servers, "warp"), [servers]);

  const tabs: Tab[] = [];
  if (regular.length) tabs.push("regular");
  if (rustleboost.length) tabs.push("rustleboost");
  if (warp.length) tabs.push("warp");

  const tabOfSelected = (): Tab => {
    const s = servers.find(x => x.id === selectedId);
    return ((s?.group as Tab) || tabs[0] || "regular");
  };
  const [tab, setTab] = useState<Tab>(tabOfSelected);
  const [collapsed, setCollapsed] = useState<string[]>([]);
  useEffect(() => { setTab(tabOfSelected()); }, [selectedId, servers.length]);
  const shown: Tab = tabs.includes(tab) ? tab : (tabs[0] ?? "regular");

  const toggle = (id: string) => setCollapsed(c => (c.includes(id) ? c.filter(x => x !== id) : [...c, id]));
  const groupIds = ["regular", "rustleboost"].filter(g => (g === "regular" ? regular : rustleboost).length);

  if (!servers.length) {
    return (
      <Card style={{ padding: 16 }}>
        <div style={{ fontSize: 14, fontWeight: 700 }}>{t("home.noServers.title")}</div>
        <div style={{ fontSize: 12, color: "var(--text-secondary)", marginTop: 4 }}>{t("home.noServers.text")}</div>
      </Card>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, paddingTop: 8 }}>
        <span style={{ fontSize: 12, fontWeight: 700, color: "var(--text-secondary)", textTransform: "uppercase" }}>{t("catalog.title")}</span>
        <span style={{ fontSize: 12, fontWeight: 700, color: "var(--text-primary)" }}>{servers.length}</span>
        <span style={{ flex: 1 }} />
        {groupIds.length > 1 && (
          <button
            onClick={() => setCollapsed(collapsed.length ? [] : groupIds)}
            style={{ fontSize: 12, fontWeight: 700, color: "var(--accent)", padding: "6px 10px", borderRadius: 8, background: "color-mix(in srgb, var(--accent) 12%, transparent)" }}
          >
            {collapsed.length ? t("catalog.expandAll") : t("catalog.collapseAll")}
          </button>
        )}
      </div>

      {tabs.length > 1 && (
        <div style={{ display: "flex", alignItems: "center", gap: 10, paddingBottom: 4 }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: "var(--text-primary)" }}>{t("catalog.picker")}</span>
          <div style={{ flex: 1 }}>
            <Segmented
              value={shown}
              onChange={setTab}
              options={tabs.map(v => ({ value: v, label: t(`catalog.tab.${v}` as const) }))}
            />
          </div>
        </div>
      )}

      <div key={shown} className="fade-in" style={{ display: "flex", flexDirection: "column", gap: 8, opacity: autoSelecting ? 0.45 : 1 }}>
        {shown === "regular" && (
          <Group id="regular" title={info?.title || t("subscription.default")} servers={regular} info={info} online={false}
            collapsed={collapsed.includes("regular")} onToggle={toggle} />
        )}
        {shown === "rustleboost" && (
          <Group id="rustleboost" title="RustleBoost" servers={rustleboost} online
            collapsed={collapsed.includes("rustleboost")} onToggle={toggle} />
        )}
        {shown === "warp" && (
          <>
            <Card style={{ overflow: "hidden" }}>
              {warp.map((s, i) => (
                <div key={s.id} style={{ borderTop: i ? "1px solid var(--separator)" : "none" }}>
                  <ServerRow server={s} online />
                </div>
              ))}
            </Card>
            <div style={{ fontSize: 12, color: "var(--text-secondary)", padding: "0 4px", lineHeight: 1.4 }}>{t("catalog.warp.hint")}</div>
          </>
        )}
      </div>
    </div>
  );
}
