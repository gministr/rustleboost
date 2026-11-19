import { Globe } from "lucide-react";

// Круглый флаг страны — набор flag-icons (MIT), квадратные версии 1x1, как
// CountryFlagBadge в приложении iOS/Mac. Если страну не определить —
// нейтральный глобус.

const flagFiles = import.meta.glob<string>("../../node_modules/flag-icons/flags/1x1/*.svg", {
  query: "?raw", import: "default", eager: true,
});

const FLAGS: Record<string, string> = Object.fromEntries(
  Object.entries(flagFiles).map(([path, svg]) => [
    path.slice(path.lastIndexOf("/") + 1, -".svg".length),
    `data:image/svg+xml,${encodeURIComponent(svg)}`,
  ]),
);

/** 🇷🇺 → "ru" */
export function emojiToCode(emoji: string): string {
  const points = Array.from(emoji || "").slice(0, 2).map(c => c.codePointAt(0) ?? 0);
  if (points.length === 2 && points.every(p => p >= 0x1f1e6 && p <= 0x1f1ff)) {
    return String.fromCharCode(points[0] - 0x1f1e6 + 65, points[1] - 0x1f1e6 + 65).toLowerCase();
  }
  return "";
}

/** «🇷🇺 LTE Россия» → «LTE Россия» */
export function displayName(name: string): string {
  return name.replace(/^[\u{1F1E6}-\u{1F1FF}]{2}\s*/u, "").trim() || name;
}

export function flagCode(server: { flag?: string; name: string }): string {
  return emojiToCode(server.flag || "") || emojiToCode(server.name);
}

export default function Flag({ code, size = 40 }: { code: string; size?: number }) {
  const src = code ? FLAGS[code.toLowerCase()] : undefined;
  return (
    <span style={{
      width: size, height: size, borderRadius: "50%", overflow: "hidden", flexShrink: 0, display: "inline-flex",
      alignItems: "center", justifyContent: "center",
      background: src ? undefined : "color-mix(in srgb, var(--accent) 18%, transparent)",
      boxShadow: "inset 0 0 0 0.5px color-mix(in srgb, var(--separator) 50%, transparent)",
    }}>
      {src
        ? <img src={src} alt="" draggable={false} style={{ width: "100%", height: "100%", objectFit: "cover" }} />
        : <Globe size={size * 0.5} style={{ color: "var(--accent)" }} />}
    </span>
  );
}
