/**
 * Flag images via SVG ?raw imports → data URI.
 * This is the only approach that reliably works in Tauri WebView2:
 * CSS url() and asset URL imports from node_modules fail in tauri:// protocol.
 */

// Import SVG content as raw strings, then convert to data URIs
import ruRaw from "flag-icons/flags/1x1/ru.svg?raw";
import nlRaw from "flag-icons/flags/1x1/nl.svg?raw";
import deRaw from "flag-icons/flags/1x1/de.svg?raw";
import plRaw from "flag-icons/flags/1x1/pl.svg?raw";
import fiRaw from "flag-icons/flags/1x1/fi.svg?raw";
import atRaw from "flag-icons/flags/1x1/at.svg?raw";
import usRaw from "flag-icons/flags/1x1/us.svg?raw";
import gbRaw from "flag-icons/flags/1x1/gb.svg?raw";
import frRaw from "flag-icons/flags/1x1/fr.svg?raw";
import seRaw from "flag-icons/flags/1x1/se.svg?raw";
import trRaw from "flag-icons/flags/1x1/tr.svg?raw";
import jpRaw from "flag-icons/flags/1x1/jp.svg?raw";
import sgRaw from "flag-icons/flags/1x1/sg.svg?raw";
import uaRaw from "flag-icons/flags/1x1/ua.svg?raw";
import czRaw from "flag-icons/flags/1x1/cz.svg?raw";
import chRaw from "flag-icons/flags/1x1/ch.svg?raw";
import itRaw from "flag-icons/flags/1x1/it.svg?raw";
import caRaw from "flag-icons/flags/1x1/ca.svg?raw";
import auRaw from "flag-icons/flags/1x1/au.svg?raw";
import noRaw from "flag-icons/flags/1x1/no.svg?raw";
import dkRaw from "flag-icons/flags/1x1/dk.svg?raw";
import huRaw from "flag-icons/flags/1x1/hu.svg?raw";
import roRaw from "flag-icons/flags/1x1/ro.svg?raw";
import ltRaw from "flag-icons/flags/1x1/lt.svg?raw";
import lvRaw from "flag-icons/flags/1x1/lv.svg?raw";
import eeRaw from "flag-icons/flags/1x1/ee.svg?raw";
import amRaw from "flag-icons/flags/1x1/am.svg?raw";
import azRaw from "flag-icons/flags/1x1/az.svg?raw";
import kzRaw from "flag-icons/flags/1x1/kz.svg?raw";
import mdRaw from "flag-icons/flags/1x1/md.svg?raw";
import bgRaw from "flag-icons/flags/1x1/bg.svg?raw";
import hrRaw from "flag-icons/flags/1x1/hr.svg?raw";
import krRaw from "flag-icons/flags/1x1/kr.svg?raw";
import hkRaw from "flag-icons/flags/1x1/hk.svg?raw";
import cnRaw from "flag-icons/flags/1x1/cn.svg?raw";

function toDataUri(svg: string): string {
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const FLAG_MAP: Record<string, string> = {
  ru: toDataUri(ruRaw), nl: toDataUri(nlRaw), de: toDataUri(deRaw),
  pl: toDataUri(plRaw), fi: toDataUri(fiRaw), at: toDataUri(atRaw),
  us: toDataUri(usRaw), gb: toDataUri(gbRaw), fr: toDataUri(frRaw),
  se: toDataUri(seRaw), tr: toDataUri(trRaw), jp: toDataUri(jpRaw),
  sg: toDataUri(sgRaw), ua: toDataUri(uaRaw), cz: toDataUri(czRaw),
  ch: toDataUri(chRaw), it: toDataUri(itRaw), ca: toDataUri(caRaw),
  au: toDataUri(auRaw), no: toDataUri(noRaw), dk: toDataUri(dkRaw),
  hu: toDataUri(huRaw), ro: toDataUri(roRaw), lt: toDataUri(ltRaw),
  lv: toDataUri(lvRaw), ee: toDataUri(eeRaw), am: toDataUri(amRaw),
  az: toDataUri(azRaw), kz: toDataUri(kzRaw), md: toDataUri(mdRaw),
  bg: toDataUri(bgRaw), hr: toDataUri(hrRaw), kr: toDataUri(krRaw),
  hk: toDataUri(hkRaw), cn: toDataUri(cnRaw),
};

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
