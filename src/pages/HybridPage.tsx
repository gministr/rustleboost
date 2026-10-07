import { useState } from "react";
import { motion } from "framer-motion";
import { useNavigate } from "react-router-dom";
import { Trash2, Plus, AlertTriangle, Zap } from "lucide-react";
import { useVPNStore } from "../store/vpnStore";
import { CustomRule } from "../api/daemon";
import { useT } from "../i18n";

/* ── Toggle (same look as SettingsPage) ───────────────────────────────── */
function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      onClick={() => onChange(!checked)}
      style={{
        width: 44, height: 24,
        borderRadius: 12,
        backgroundColor: checked ? "#0ea5e9" : "var(--c-titlebar-btn)",
        position: "relative",
        flexShrink: 0,
        border: "none",
        cursor: "pointer",
        transition: "background-color 0.2s",
      }}
    >
      <div style={{
        position: "absolute",
        top: 2, left: checked ? 22 : 2,
        width: 20, height: 20,
        borderRadius: 10,
        backgroundColor: "white",
        boxShadow: "0 1px 4px rgba(0,0,0,0.3)",
        transition: "left 0.2s",
      }} />
    </button>
  );
}

function Sec({ title }: { title: string }) {
  return (
    <p style={{
      fontSize: 11, letterSpacing: "0.1em", textTransform: "uppercase",
      color: "var(--c-sec-label)", fontWeight: 600,
      paddingTop: 20, paddingBottom: 8,
    }}>{title}</p>
  );
}

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      background: "var(--c-surface)",
      border: "1px solid rgba(255,255,255,0.07)",
      borderRadius: 14, padding: 16, marginBottom: 8,
    }}>
      {children}
    </div>
  );
}

const RULE_TYPES: { value: CustomRule["type"]; labelKey: "ruleTypeDomain" | "ruleTypeSuffix" | "ruleTypeCidr" | "ruleTypeProcess" }[] = [
  { value: "domain", labelKey: "ruleTypeDomain" },
  { value: "domain_suffix", labelKey: "ruleTypeSuffix" },
  { value: "ip_cidr", labelKey: "ruleTypeCidr" },
  { value: "process_name", labelKey: "ruleTypeProcess" },
];

export default function HybridPage() {
  const { settings, saveSettings, gameProfiles, status } = useVPNStore();
  const t = useT();
  const navigate = useNavigate();

  const [newRuleType, setNewRuleType] = useState<CustomRule["type"]>("domain");
  const [newRuleValue, setNewRuleValue] = useState("");

  const hybridOn = settings.route_mode === "hybrid";
  const games = settings.hybrid_games ?? [];
  const customRules = settings.hybrid_custom_rules ?? [];

  const setHybridOn = (on: boolean) => {
    saveSettings({ route_mode: on ? "hybrid" : "ru" });
  };

  const toggleGame = (id: string) => {
    const next = games.includes(id) ? games.filter(g => g !== id) : [...games, id];
    saveSettings({ hybrid_games: next });
  };

  const setZapret = (on: boolean) => saveSettings({ hybrid_zapret: on });

  const addRule = () => {
    const value = newRuleValue.trim();
    if (!value) return;
    saveSettings({ hybrid_custom_rules: [...customRules, { type: newRuleType, value }] });
    setNewRuleValue("");
  };

  const removeRule = (index: number) => {
    saveSettings({ hybrid_custom_rules: customRules.filter((_, i) => i !== index) });
  };

  const currentServerName = status.server?.name ?? t("hybridNoServer");

  return (
    <motion.div
      style={{ display: "flex", flexDirection: "column", height: "100%" }}
      initial={{ opacity: 0, x: 20 }}
      animate={{ opacity: 1, x: 0 }}
      exit={{ opacity: 0, x: 20 }}
    >
      <div style={{ padding: "8px 16px 4px", flexShrink: 0 }}>
        <h2 style={{ fontSize: 16, fontWeight: 600, margin: 0 }}>{t("navHybrid")}</h2>
      </div>

      <div style={{ flex: 1, overflowY: "auto", padding: "0 16px 24px" }}>

        {/* ── Главный переключатель ── */}
        <Sec title={t("hybridMain")} />
        <Card>
          <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
            <div style={{ flex: 1 }}>
              <p style={{ fontSize: 14, fontWeight: 600, color: "var(--c-text)", margin: 0 }}>
                {t("hybridEnable")}
              </p>
              <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "4px 0 0", lineHeight: 1.5 }}>
                {t("hybridEnableSub")}
              </p>
            </div>
            <Toggle checked={hybridOn} onChange={setHybridOn} />
          </div>
          <p style={{ fontSize: 11, color: "var(--c-text-dim)", margin: "10px 0 0" }}>
            {t("appliesNextConnect")}
          </p>
        </Card>

        {hybridOn && (
          <>
            {/* ── Zapret: Discord/YouTube напрямую ── */}
            <Sec title={t("hybridZapretSec")} />
            <Card>
              <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                <div style={{
                  width: 36, height: 36, borderRadius: 10,
                  background: "var(--c-surface-hover)",
                  display: "flex", alignItems: "center", justifyContent: "center", flexShrink: 0,
                }}>
                  <Zap size={16} style={{ color: "var(--c-text-sub)" }} />
                </div>
                <div style={{ flex: 1 }}>
                  <p style={{ fontSize: 14, fontWeight: 500, color: "var(--c-text)", margin: 0 }}>
                    {t("hybridZapret")}
                  </p>
                  <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "2px 0 0", lineHeight: 1.5 }}>
                    {t("hybridZapretSub")}
                  </p>
                </div>
                <Toggle checked={settings.hybrid_zapret} onChange={setZapret} />
              </div>

              <div style={{
                display: "flex", gap: 8, alignItems: "flex-start",
                marginTop: 12, padding: "10px 12px",
                background: "rgba(234,179,8,0.08)", border: "1px solid rgba(234,179,8,0.25)",
                borderRadius: 10,
              }}>
                <AlertTriangle size={14} style={{ color: "#eab308", flexShrink: 0, marginTop: 2 }} />
                <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: 0, lineHeight: 1.5 }}>
                  {t("hybridZapretWarn")}
                </p>
              </div>

              {settings.hybrid_zapret && (
                <p style={{ fontSize: 11, margin: "10px 0 0", lineHeight: 1.5 }}>
                  {status.zapret_active ? (
                    <span style={{ color: "#4ade80" }}>● {t("hybridZapretOn")}</span>
                  ) : status.zapret_warning ? (
                    <span style={{ color: "#f87171" }}>● {status.zapret_warning}</span>
                  ) : (
                    <span style={{ color: "var(--c-text-dim)" }}>● {t("hybridZapretPending")}</span>
                  )}
                </p>
              )}
            </Card>

            {/* ── Игры напрямую ── */}
            <Sec title={t("hybridGamesSec")} />
            <Card>
              <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "0 0 12px", lineHeight: 1.5 }}>
                {t("hybridGamesSub")}
              </p>
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                {gameProfiles.map(g => {
                  const active = games.includes(g.id);
                  return (
                    <button
                      key={g.id}
                      onClick={() => toggleGame(g.id)}
                      style={{
                        display: "flex", alignItems: "center", gap: 12,
                        padding: "10px 14px", borderRadius: 10, cursor: "pointer",
                        background: active ? "rgba(14,165,233,0.15)" : "rgba(255,255,255,0.04)",
                        border: `1px solid ${active ? "rgba(14,165,233,0.45)" : "var(--c-surface-hover)"}`,
                        transition: "all 0.15s", textAlign: "left",
                      }}
                    >
                      <div style={{
                        width: 16, height: 16, borderRadius: 4, flexShrink: 0,
                        border: `2px solid ${active ? "#0ea5e9" : "var(--c-text-dim)"}`,
                        background: active ? "#0ea5e9" : "transparent",
                        display: "flex", alignItems: "center", justifyContent: "center",
                      }}>
                        {active && <div style={{ width: 7, height: 7, background: "white", borderRadius: 2 }} />}
                      </div>
                      <p style={{ fontSize: 13, fontWeight: 500, color: active ? "#38bdf8" : "var(--c-text)", margin: 0 }}>
                        {g.name}
                      </p>
                    </button>
                  );
                })}
                {gameProfiles.length === 0 && (
                  <p style={{ fontSize: 12, color: "var(--c-text-dim)" }}>{t("loading")}</p>
                )}
              </div>
            </Card>

            {/* ── Свои правила ── */}
            <Sec title={t("hybridCustomSec")} />
            <Card>
              <p style={{ fontSize: 11, color: "var(--c-text-sub)", margin: "0 0 12px", lineHeight: 1.5 }}>
                {t("hybridCustomSub")}
              </p>

              {customRules.length > 0 && (
                <div style={{ display: "flex", flexDirection: "column", gap: 6, marginBottom: 12 }}>
                  {customRules.map((rule, i) => (
                    <div key={i} style={{
                      display: "flex", alignItems: "center", gap: 8,
                      padding: "8px 12px", borderRadius: 10,
                      background: "rgba(255,255,255,0.04)", border: "1px solid var(--c-surface-hover)",
                    }}>
                      <span style={{
                        fontSize: 10, fontWeight: 600, padding: "2px 7px", borderRadius: 6,
                        background: "var(--c-surface-hover)", color: "var(--c-text-sub)", flexShrink: 0,
                      }}>
                        {t(RULE_TYPES.find(r => r.value === rule.type)?.labelKey ?? "ruleTypeDomain")}
                      </span>
                      <span style={{ fontSize: 13, color: "var(--c-text)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {rule.value}
                      </span>
                      <button
                        onClick={() => removeRule(i)}
                        style={{ background: "none", border: "none", cursor: "pointer", color: "var(--c-text-dim)", padding: 2, flexShrink: 0 }}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  ))}
                </div>
              )}

              <div style={{ display: "flex", gap: 8 }}>
                <select
                  value={newRuleType}
                  onChange={e => setNewRuleType(e.target.value as CustomRule["type"])}
                  style={{
                    background: "var(--c-surface-hover)", border: "1px solid var(--c-border)",
                    borderRadius: 10, padding: "8px 8px", fontSize: 12, color: "var(--c-text)",
                    outline: "none", cursor: "pointer", flexShrink: 0,
                  }}
                >
                  {RULE_TYPES.map(rt => (
                    <option key={rt.value} value={rt.value}>{t(rt.labelKey)}</option>
                  ))}
                </select>
                <input
                  type="text"
                  value={newRuleValue}
                  onChange={e => setNewRuleValue(e.target.value)}
                  onKeyDown={e => e.key === "Enter" && addRule()}
                  placeholder={t("hybridCustomPlaceholder")}
                  style={{
                    flex: 1, minWidth: 0, padding: "8px 12px",
                    fontSize: 13, borderRadius: 10,
                    background: "var(--c-border)", border: "1px solid rgba(255,255,255,0.12)",
                    color: "var(--c-text)", outline: "none",
                  }}
                />
                <button
                  onClick={addRule}
                  style={{
                    width: 36, height: 36, borderRadius: 10, flexShrink: 0,
                    background: "#0ea5e9", border: "none", color: "white",
                    cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center",
                  }}
                >
                  <Plus size={16} />
                </button>
              </div>
            </Card>

            {/* ── Остальной трафик ── */}
            <Sec title={t("hybridRestSec")} />
            <Card>
              <p style={{ fontSize: 12, color: "var(--c-text-sub)", margin: "0 0 10px", lineHeight: 1.5 }}>
                {t("hybridRestSub")}
              </p>
              <div style={{
                display: "flex", alignItems: "center", gap: 10,
                padding: "10px 12px", borderRadius: 10,
                background: "var(--c-surface-hover)",
              }}>
                <span style={{ fontSize: 13, color: "var(--c-text)", flex: 1 }}>
                  {t("hybridCurrentServer")}: <strong>{currentServerName}</strong>
                </span>
                <button
                  onClick={() => navigate("/")}
                  style={{
                    fontSize: 12, fontWeight: 600, padding: "6px 12px", borderRadius: 8,
                    background: "#0ea5e9", border: "none", color: "white", cursor: "pointer",
                    flexShrink: 0,
                  }}
                >
                  {t("hybridPickServer")}
                </button>
              </div>
            </Card>
          </>
        )}
      </div>
    </motion.div>
  );
}
