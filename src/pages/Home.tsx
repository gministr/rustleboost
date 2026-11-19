import { useEffect, useRef, useState } from "react";
import ConnectionSection from "../components/Connection";
import ServerCatalog from "../components/ServerCatalog";
import { MessageBanner, UpdateBanner } from "../components/Banners";

// Главный экран — как HomeView + MacAdaptiveHome в приложении на Mac: в узком
// окне одна колонка, в широком подключение и каталог стоят рядом, у каждого
// своя прокрутка, и кнопка остаётся на виду, пока листаешь серверы.

const TWO_COLUMN_WIDTH = 860;

export default function Home() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(400);
  useEffect(() => {
    if (!ref.current) return;
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width));
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, []);

  const connection = (
    <>
      <UpdateBanner />
      <MessageBanner />
      <ConnectionSection />
    </>
  );

  return (
    <div ref={ref} style={{ flex: 1, minHeight: 0, display: "flex" }}>
      {width >= TWO_COLUMN_WIDTH ? (
        <>
          <div className="scroll" style={{ width: Math.min(480, width * 0.42), padding: "0 24px 32px", display: "flex", flexDirection: "column", gap: 24 }}>
            {connection}
          </div>
          <div style={{ width: 1, background: "var(--separator)" }} />
          <div className="scroll" style={{ flex: 1, padding: "0 24px 32px" }}>
            <div style={{ maxWidth: 820, margin: "0 auto" }}><ServerCatalog /></div>
          </div>
        </>
      ) : (
        <div className="scroll" style={{ flex: 1, padding: "0 16px 32px" }}>
          <div style={{ maxWidth: 640, margin: "0 auto", display: "flex", flexDirection: "column", gap: 24 }}>
            {connection}
            <ServerCatalog />
          </div>
        </div>
      )}
    </div>
  );
}
