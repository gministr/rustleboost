import { useEffect, useRef, useState } from "react";
import type { UiStatus } from "../state/store";
import { useIsDark } from "../lib/theme";
import frameCounts from "../assets/mascot/frames.json";

// Талисман — те же кадры и та же логика, что MascotView в приложении
// iOS/Mac: сидит (покой), встаёт (подключение), бежит (подключено),
// тормозит (отключение). Соседние кадры смешиваются по дробной позиции —
// движение плавное при любой частоте экрана.

export type Phase = "sit" | "rise" | "run" | "stop";
type Playback = "loop" | "pingPong" | "once";

const PHASES: Record<Phase, { fps: number; playback: Playback }> = {
  sit: { fps: 21.5, playback: "pingPong" },
  rise: { fps: 23, playback: "once" },
  run: { fps: 23, playback: "loop" },
  stop: { fps: 23, playback: "once" },
};

const urls = import.meta.glob<string>("../assets/mascot/*/*/*.webp", { query: "?url", import: "default", eager: true });

function framesFor(phase: Phase, theme: "light" | "dark"): string[] {
  const count = (frameCounts as Record<string, number>)[phase] ?? 0;
  const list: string[] = [];
  for (let i = 1; i <= count; i++) {
    const key = `../assets/mascot/${phase}/${theme}/${String(i).padStart(3, "0")}.webp`;
    if (urls[key]) list.push(urls[key]);
  }
  return list;
}

// Кадры декодируются заранее: иначе в момент подключения анимация
// спотыкалась бы на загрузке.
const preloaded = new Set<string>();
function preload(list: string[]) {
  for (const src of list) {
    if (preloaded.has(src)) continue;
    preloaded.add(src);
    const img = new Image();
    img.src = src;
    img.decode?.().catch(() => {});
  }
}

export function phaseFor(status: UiStatus): Phase {
  switch (status) {
    case "connecting": return "rise";
    case "connected": return "run";
    case "disconnecting": return "stop";
    default: return "sit";
  }
}

/** Длительность одного цикла бега — по ней планета катится со скоростью шага. */
export const strideDuration = ((frameCounts as Record<string, number>).run || 14) / PHASES.run.fps;

export default function Mascot({ status, height = 132 }: { status: UiStatus; height?: number }) {
  const isDark = useIsDark();
  const theme = isDark ? "dark" : "light";
  const target = phaseFor(status);
  const [phase, setPhase] = useState<Phase>(target);
  const startedAt = useRef(performance.now());
  const a = useRef<HTMLImageElement>(null);
  const b = useRef<HTMLImageElement>(null);

  useEffect(() => {
    (["sit", "rise", "run", "stop"] as Phase[]).forEach(p => preload(framesFor(p, theme)));
  }, [theme]);

  // Переход между фазами: однократная (встаёт/тормозит) доигрывает до конца.
  useEffect(() => {
    if (target === phase) return;
    const current = PHASES[phase];
    let wait = 0;
    if (current.playback === "once") {
      const duration = (framesFor(phase, theme).length / current.fps) * 1000;
      wait = Math.max(0, duration - (performance.now() - startedAt.current));
    }
    const timer = setTimeout(() => {
      setPhase(target);
      startedAt.current = performance.now();
    }, wait);
    return () => clearTimeout(timer);
  }, [target, phase, theme]);

  useEffect(() => {
    const frames = framesFor(phase, theme);
    const { fps, playback } = PHASES[phase];
    if (!frames.length) return;
    let raf = 0;
    const tick = () => {
      const elapsed = (performance.now() - startedAt.current) / 1000;
      let pos = elapsed * fps;
      const n = frames.length;
      if (playback === "loop") pos = pos % n;
      else if (playback === "pingPong") {
        const span = Math.max(1, n - 1);
        const cycle = pos % (span * 2);
        pos = cycle <= span ? cycle : span * 2 - cycle;
      } else pos = Math.min(pos, n - 1);
      const index = Math.min(n - 1, Math.floor(pos));
      const next = playback === "loop" ? (index + 1) % n : Math.min(n - 1, index + 1);
      const blend = pos - index;
      if (a.current && b.current) {
        if (a.current.dataset.src !== frames[index]) { a.current.src = frames[index]; a.current.dataset.src = frames[index]; }
        if (b.current.dataset.src !== frames[next]) { b.current.src = frames[next]; b.current.dataset.src = frames[next]; }
        b.current.style.opacity = String(blend);
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [phase, theme]);

  const style = { position: "absolute" as const, inset: 0, width: "100%", height: "100%", objectFit: "contain" as const };
  return (
    <div key={phase} className="fade-in" style={{ position: "relative", height, width: height, pointerEvents: "none" }} aria-hidden>
      <img ref={a} alt="" draggable={false} style={style} />
      <img ref={b} alt="" draggable={false} style={{ ...style, opacity: 0 }} />
    </div>
  );
}
