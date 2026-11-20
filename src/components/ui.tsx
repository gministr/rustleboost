import { ReactNode, CSSProperties } from "react";
import { Check, ChevronRight, LoaderCircle } from "lucide-react";

// Базовые элементы — как DesignSystem приложения iOS/Mac: карточка,
// переключатель, сегменты, строки настроек, кнопки.

export function Card({ children, style, className = "" }: { children: ReactNode; style?: CSSProperties; className?: string }) {
  return <div className={`card ${className}`} style={style}>{children}</div>;
}

export function Spinner({ size = 14 }: { size?: number }) {
  return <LoaderCircle size={size} className="spin" style={{ color: "var(--text-secondary)" }} />;
}

/** Переключатель RustleBoost — свой, не как на iPhone и не как в Windows:
 *  скруглённый прямоугольник с квадратной «клавишей». Включён — фирменная
 *  заливка и отметка на клавише, выключен — спокойная подложка с точкой. */
export function Toggle({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`rb-toggle${checked ? " on" : ""}`}
    >
      <span className="rb-toggle-key">
        {checked ? <Check size={12} strokeWidth={3.2} /> : <span className="rb-toggle-dot" />}
      </span>
    </button>
  );
}

/** Выбор из нескольких вариантов — ряд «клавиш»: выбранная подсвечена
 *  фирменным цветом (не серая «пилюля» iPhone). */
export function Segmented<T extends string>({ value, options, onChange }: {
  value: T; options: { value: T; label: string }[]; onChange: (v: T) => void;
}) {
  return (
    <div className="rb-segmented" role="radiogroup">
      {options.map(o => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.value)}
            className={`rb-segment${active ? " on" : ""}`}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <div style={{ fontSize: 12, fontWeight: 700, color: "var(--text-secondary)", textTransform: "uppercase", letterSpacing: 0.3, padding: "18px 16px 6px" }}>
      {children}
    </div>
  );
}

/** Группа строк настроек на одной карточке. */
export function SettingsGroup({ title, footer, children }: { title?: string; footer?: ReactNode; children: ReactNode }) {
  return (
    <div>
      {title && <SectionTitle>{title}</SectionTitle>}
      <Card style={{ overflow: "hidden" }}>{children}</Card>
      {footer && <div style={{ fontSize: 12, color: "var(--text-secondary)", padding: "6px 16px 0", lineHeight: 1.4 }}>{footer}</div>}
    </div>
  );
}

export function SettingsRow({ icon, title, subtitle, trailing, onClick, danger, last }: {
  icon?: ReactNode; title: ReactNode; subtitle?: ReactNode; trailing?: ReactNode;
  onClick?: () => void; danger?: boolean; last?: boolean;
}) {
  const content = (
    <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "11px 16px", minHeight: 48 }}>
      {icon && <span style={{ color: danger ? "var(--danger)" : "var(--accent)", display: "flex", flexShrink: 0 }}>{icon}</span>}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 14, fontWeight: 600, color: danger ? "var(--danger)" : "var(--text-primary)" }}>{title}</div>
        {subtitle && <div style={{ fontSize: 12, color: "var(--text-secondary)", marginTop: 2, lineHeight: 1.35 }}>{subtitle}</div>}
      </div>
      {trailing}
      {onClick && !trailing && <ChevronRight size={16} style={{ color: "var(--text-secondary)", opacity: 0.6 }} />}
    </div>
  );
  return (
    <div style={{ borderBottom: last ? "none" : "1px solid var(--separator)" }}>
      {onClick ? (
        <button onClick={onClick} style={{ width: "100%", textAlign: "left", display: "block" }} className="row-hover">{content}</button>
      ) : content}
    </div>
  );
}

export function PrimaryButton({ children, onClick, disabled, loading }: {
  children: ReactNode; onClick: () => void; disabled?: boolean; loading?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled || loading}
      style={{
        width: "100%", height: 48, borderRadius: 16, fontSize: 15, fontWeight: 700, color: "#fff",
        background: "var(--accent)", opacity: disabled || loading ? 0.45 : 1,
        display: "flex", alignItems: "center", justifyContent: "center", gap: 8,
      }}
    >
      {loading ? <LoaderCircle size={18} className="spin" /> : children}
    </button>
  );
}

export function IconButton({ children, onClick, title, disabled, busy, size = 28, color = "var(--accent)" }: {
  children: ReactNode; onClick: () => void; title?: string; disabled?: boolean; busy?: boolean; size?: number; color?: string;
}) {
  return (
    <button
      onClick={e => { e.stopPropagation(); onClick(); }}
      title={title}
      aria-label={title}
      disabled={disabled || busy}
      style={{
        width: size, height: size, display: "flex", alignItems: "center", justifyContent: "center",
        color, opacity: disabled && !busy ? 0.35 : 1, borderRadius: 8,
      }}
    >
      {busy ? <Spinner size={14} /> : children}
    </button>
  );
}

/** Круглая кнопка панели — как кнопки панели инструментов на Mac. */
export function ToolbarButton({ children, onClick, title, badge }: { children: ReactNode; onClick: () => void; title: string; badge?: number }) {
  return (
    <button
      onClick={onClick}
      title={title}
      aria-label={title}
      className="no-drag"
      style={{
        width: 34, height: 34, borderRadius: 17, display: "flex", alignItems: "center", justifyContent: "center",
        background: "var(--surface-elevated)", color: "var(--text-secondary)", position: "relative", flexShrink: 0,
      }}
    >
      {children}
      {!!badge && (
        <span style={{
          position: "absolute", top: -2, right: -2, minWidth: 16, height: 16, borderRadius: 8, padding: "0 4px",
          background: "var(--danger)", color: "#fff", fontSize: 10, fontWeight: 800,
          display: "flex", alignItems: "center", justifyContent: "center",
        }}>{badge > 9 ? "9+" : badge}</span>
      )}
    </button>
  );
}
