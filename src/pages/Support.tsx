import { useEffect, useRef, useState } from "react";
import { ChevronRight, MessageSquarePlus, Send, Trash2 } from "lucide-react";
import { Card, SectionTitle, Spinner } from "../components/ui";
import { api, errorText, SupportNotice, SupportTicket } from "../lib/api";
import { useApp } from "../state/store";
import { useT } from "../lib/strings";

// Поддержка — как SupportView и TicketView в приложении iOS/Mac: сообщения
// от сервиса (рассылки из админки), обращения и переписка по ним.

function when(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString("ru-RU", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function Composer({ placeholder, onSend, disabled }: { placeholder: string; onSend: (text: string) => Promise<void>; disabled?: boolean }) {
  const t = useT();
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const send = async () => {
    if (!text.trim() || sending) return;
    setSending(true);
    try { await onSend(text.trim()); setText(""); } finally { setSending(false); }
  };
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "flex-end" }}>
      <textarea
        value={text}
        onChange={e => setText(e.target.value)}
        onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } }}
        placeholder={placeholder}
        rows={2}
        disabled={disabled}
        style={{ flex: 1, resize: "none", padding: "10px 12px", borderRadius: 14, outline: "none", background: "var(--surface-elevated)", border: "1px solid var(--separator)", fontSize: 13, lineHeight: 1.4 }}
      />
      <button onClick={send} disabled={!text.trim() || sending || disabled} aria-label={t("support.send")}
        style={{ width: 40, height: 40, borderRadius: 20, background: "var(--accent)", color: "#fff", display: "flex", alignItems: "center", justifyContent: "center", opacity: !text.trim() || disabled ? 0.45 : 1, flexShrink: 0 }}>
        {sending ? <Spinner size={16} /> : <Send size={16} />}
      </button>
    </div>
  );
}

function TicketView({ id, onBack }: { id: number; onBack: () => void }) {
  const t = useT();
  const [ticket, setTicket] = useState<SupportTicket | null>(null);
  const [error, setError] = useState<string | null>(null);
  const end = useRef<HTMLDivElement>(null);
  const load = async () => {
    try { setTicket(await api.ticket(id)); setError(null); } catch (e) { setError(errorText(e, t("support.loadFailed"))); }
  };
  useEffect(() => { load(); const timer = setInterval(load, 20_000); return () => clearInterval(timer); }, [id]);
  useEffect(() => { end.current?.scrollIntoView({ block: "end" }); }, [ticket?.messages?.length]);
  const reply = async (text: string) => {
    try { setTicket(await api.reply(id, text)); setError(null); }
    catch (e) { setError(errorText(e, t("support.error.send"))); throw e; }
  };
  const closed = ticket?.status === "closed";
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <button onClick={onBack} style={{ alignSelf: "flex-start", color: "var(--accent)", fontWeight: 700, fontSize: 13, padding: "8px 0" }}>← {t("support.section.tickets")}</button>
      <div style={{ fontSize: 18, fontWeight: 800 }}>{t("support.ticketNumber", { n: id })}</div>
      {!ticket && !error && <Spinner />}
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {ticket?.messages?.map(m => {
          const mine = m.author === "user";
          return (
            <div key={m.id} style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "82%" }}>
              <div className="selectable" style={{
                padding: "9px 12px", borderRadius: 14, fontSize: 13, lineHeight: 1.4, whiteSpace: "pre-wrap",
                background: mine ? "var(--accent)" : "var(--surface)", color: mine ? "#fff" : "var(--text-primary)",
                boxShadow: mine ? "none" : "var(--card-shadow)",
              }}>{m.body}</div>
              <div style={{ fontSize: 10, color: "var(--text-secondary)", marginTop: 3, textAlign: mine ? "right" : "left" }}>
                {mine ? t("support.author.user") : t("support.author.admin")} · {when(m.createdAt)}
              </div>
            </div>
          );
        })}
        <div ref={end} />
      </div>
      {error && <div style={{ fontSize: 12, color: "var(--danger)" }}>{error}</div>}
      {closed
        ? <div style={{ fontSize: 12, color: "var(--text-secondary)", lineHeight: 1.4 }}>{t("support.closedNote")}</div>
        : ticket && <Composer placeholder={t("support.messagePlaceholder")} onSend={reply} />}
    </div>
  );
}

export default function SupportPage() {
  const t = useT();
  const refreshInbox = useApp(s => s.refreshInbox);
  const [tickets, setTickets] = useState<SupportTicket[] | null>(null);
  const [notices, setNotices] = useState<SupportNotice[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [openId, setOpenId] = useState<number | null>(null);
  const [composing, setComposing] = useState(false);

  const load = async () => {
    try {
      const [ts, ns] = await Promise.all([api.tickets(), api.notices()]);
      setTickets(ts); setNotices(ns); setError(null);
      if (ns.some(n => !n.read)) api.markNoticesRead().then(refreshInbox).catch(() => {});
    } catch (e) {
      setError(errorText(e, t("support.loadFailed")));
      setTickets(tickets ?? []);
    }
  };
  useEffect(() => { load(); }, []);

  const create = async (text: string) => {
    try {
      const ticket = await api.createTicket(text);
      setComposing(false);
      setOpenId(ticket.id);
      load();
    } catch (e) {
      setError(errorText(e, t("support.error.send")));
      throw e;
    }
  };
  const removeNotice = async (id?: number) => {
    try { await api.deleteNotices(id); setNotices(id ? notices.filter(n => n.id !== id) : []); } catch { /* */ }
  };

  return (
    <div className="scroll" style={{ flex: 1, padding: "0 16px 32px" }}>
      <div style={{ maxWidth: 640, margin: "0 auto", display: "flex", flexDirection: "column", gap: 6 }}>
        {openId !== null ? (
          <TicketView id={openId} onBack={() => { setOpenId(null); load(); refreshInbox(); }} />
        ) : (
          <>
            {error && <div style={{ fontSize: 12, color: "var(--danger)", padding: "8px 4px" }}>{error}</div>}

            <SectionTitle>{t("support.section.notices")}</SectionTitle>
            <Card style={{ overflow: "hidden" }}>
              {notices.length === 0 && <div style={{ padding: 16, fontSize: 13, color: "var(--text-secondary)" }}>{t("support.noNotices")}</div>}
              {notices.map((n, i) => (
                <div key={n.id} style={{ padding: "12px 16px", borderTop: i ? "1px solid var(--separator)" : "none", display: "flex", gap: 10 }}>
                  <div style={{ flex: 1 }}>
                    <div className="selectable" style={{ fontSize: 13, lineHeight: 1.45, whiteSpace: "pre-wrap", fontWeight: n.read ? 500 : 700 }}>{n.body}</div>
                    <div style={{ fontSize: 10, color: "var(--text-secondary)", marginTop: 4 }}>{when(n.createdAt)}</div>
                  </div>
                  <button onClick={() => removeNotice(n.id)} title={t("support.notices.delete")} style={{ color: "var(--text-secondary)", display: "flex", alignSelf: "flex-start" }}><Trash2 size={14} /></button>
                </div>
              ))}
            </Card>
            {notices.length > 1 && (
              <button onClick={() => removeNotice()} style={{ alignSelf: "flex-start", fontSize: 12, fontWeight: 700, color: "var(--danger)", padding: "6px 4px" }}>{t("support.notices.clear")}</button>
            )}

            <SectionTitle>{t("support.section.tickets")}</SectionTitle>
            <Card style={{ overflow: "hidden" }}>
              {composing ? (
                <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 10 }}>
                  <div style={{ fontSize: 14, fontWeight: 800 }}>{t("support.newTicket")}</div>
                  <div style={{ fontSize: 12, color: "var(--text-secondary)", lineHeight: 1.4 }}>{t("support.newTicket.hint")}</div>
                  <Composer placeholder={t("support.messagePlaceholder")} onSend={create} />
                  <button onClick={() => setComposing(false)} style={{ alignSelf: "flex-start", fontSize: 12, color: "var(--text-secondary)", fontWeight: 700 }}>{t("common.cancel")}</button>
                </div>
              ) : (
                <button onClick={() => setComposing(true)} className="row-hover" style={{ width: "100%", display: "flex", alignItems: "center", gap: 10, padding: "13px 16px", color: "var(--accent)", fontWeight: 700, fontSize: 14 }}>
                  <MessageSquarePlus size={18} />{t("support.newTicket")}
                </button>
              )}
              {tickets === null && <div style={{ padding: 16 }}><Spinner /></div>}
              {tickets?.length === 0 && !composing && (
                <div style={{ padding: "12px 16px", borderTop: "1px solid var(--separator)", fontSize: 13, color: "var(--text-secondary)" }}>{t("support.empty")}</div>
              )}
              {tickets?.map(ticket => (
                <button key={ticket.id} onClick={() => setOpenId(ticket.id)} className="row-hover"
                  style={{ width: "100%", textAlign: "left", display: "flex", alignItems: "center", gap: 10, padding: "12px 16px", borderTop: "1px solid var(--separator)" }}>
                  {ticket.unread && <span style={{ width: 8, height: 8, borderRadius: 4, background: "var(--accent)", flexShrink: 0 }} />}
                  <span style={{ flex: 1, minWidth: 0 }}>
                    <span style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
                      <span style={{ fontSize: 14, fontWeight: 700 }}>{t("support.ticketNumber", { n: ticket.id })}</span>
                      <span style={{ fontSize: 11, fontWeight: 700, color: ticket.status === "closed" ? "var(--text-secondary)" : "var(--connected)" }}>
                        {ticket.status === "closed" ? t("support.status.closed") : t("support.status.open")}
                      </span>
                    </span>
                    <span style={{ display: "block", fontSize: 12, color: "var(--text-secondary)", marginTop: 2, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{ticket.preview}</span>
                  </span>
                  <span style={{ fontSize: 10, color: "var(--text-secondary)" }}>{when(ticket.updatedAt)}</span>
                  <ChevronRight size={15} style={{ color: "var(--text-secondary)", opacity: 0.6 }} />
                </button>
              ))}
            </Card>
            <div style={{ fontSize: 11, color: "var(--text-secondary)", padding: "6px 4px", lineHeight: 1.4 }}>{t("support.retentionHint")}</div>
          </>
        )}
      </div>
    </div>
  );
}
