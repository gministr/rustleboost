/**
 * Flag images via SVG ?raw imports → data URI.
 * This is the only approach that reliably works in Tauri WebView2:
 * CSS url() and asset URL imports from node_modules fail in tauri:// protocol.
 *
 * Every flag in the set is pulled in at build time rather than listed by
 * hand. The hand-written list silently omitted whatever nobody had thought
 * to add — Spain and the EU among them — and each new location would have
 * needed a code change to get an icon.
 */
const flagFiles = import.meta.glob<string>(
  "../../node_modules/flag-icons/flags/1x1/*.svg",
  { query: "?raw", import: "default", eager: true },
);

function toDataUri(svg: string): string {
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const FLAG_MAP: Record<string, string> = Object.fromEntries(
  Object.entries(flagFiles).map(([path, svg]) => {
    const code = path.slice(path.lastIndexOf("/") + 1, -".svg".length);
    return [code, toDataUri(svg)];
  }),
);

/** Convert regional-indicator emoji (🇷🇺) → ISO code (ru) */
export function emojiToCode(emoji: string): string {
  if (!emoji) return "";
  const pts: number[] = [];
  for (const ch of emoji) {
    const cp = ch.codePointAt(0);
    if (cp !== undefined) pts.push(cp);
    if (pts.length >= 2) break;
  }
  if (pts.length >= 2 && pts[0] >= 0x1F1E6 && pts[0] <= 0x1F1FF)
    return String.fromCharCode(pts[0] - 0x1F1E6 + 65, pts[1] - 0x1F1E6 + 65).toLowerCase();
  return "";
}

/** Extract leading flag emoji from server name e.g. "🇷🇺 LTE Россия" → "🇷🇺" */
export function extractNameEmoji(name: string): string {
  const pts: number[] = [];
  for (const ch of name) {
    const cp = ch.codePointAt(0);
    if (cp === undefined) break;
    if (pts.length >= 2) break;
    pts.push(cp);
  }
  if (
    pts.length >= 2 &&
    pts[0] >= 0x1F1E6 && pts[0] <= 0x1F1FF &&
    pts[1] >= 0x1F1E6 && pts[1] <= 0x1F1FF
  ) {
    return String.fromCodePoint(pts[0]) + String.fromCodePoint(pts[1]);
  }
  return "";
}

/** Strip leading flag emoji + space: "🇷🇺 LTE Россия" → "LTE Россия" */
export function stripNameEmoji(name: string): string {
  // Exactly 2 regional-indicator code points followed by optional space(s)
  return name.replace(/^[\u{1F1E6}-\u{1F1FF}]{2}\s*/u, "");
}

interface Props {
  flag: string;
  name: string;
  country: string;
  size?: number;
}

export default function FlagImage({ flag, name, country, size = 44 }: Props) {
  // Prefer emoji extracted from name (more reliable than the flag field from daemon)
  const nameEmoji = extractNameEmoji(name);
  const bestEmoji = nameEmoji || flag;
  const code = emojiToCode(bestEmoji);
  const src = FLAG_MAP[code];

  if (src) {
    return (
      <div style={{
        width: size, height: size, borderRadius: "50%", flexShrink: 0,
        overflow: "hidden",
        border: "1px solid rgba(255,255,255,0.14)",
        background: "#111826",
      }}>
        <img
          src={src}
          alt={country}
          draggable={false}
          style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }}
        />
      </div>
    );
  }

  // Fallback: 2-letter code in circle
  const codeDisplay = code.toUpperCase() || "??";
  return (
    <div style={{
      width: size, height: size, borderRadius: "50%", flexShrink: 0,
      background: "rgba(37,99,235,0.2)",
      border: "1px solid rgba(37,99,235,0.35)",
      display: "flex", alignItems: "center", justifyContent: "center",
    }}>
      <span style={{
        fontSize: Math.round(size * 0.3), fontWeight: 800,
        color: "rgba(96,165,250,0.9)", fontFamily: "monospace",
      }}>
        {codeDisplay}
      </span>
    </div>
  );
}
