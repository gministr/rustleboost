// Словесный знак: «rustleboost» основным цветом и «.online» вторичным —
// как Wordmark в приложении iOS/Mac.
export default function Wordmark({ size = 18 }: { size?: number }) {
  return (
    <span style={{ fontSize: size, fontWeight: 800, letterSpacing: -0.3, whiteSpace: "nowrap", fontFamily: "'Segoe UI Variable Display', 'Segoe UI', system-ui, sans-serif" }}>
      <span style={{ color: "var(--text-primary)" }}>rustleboost</span>
      <span style={{ color: "var(--text-secondary)" }}>.online</span>
    </span>
  );
}
