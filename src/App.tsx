import { useEffect } from "react";
import Toolbar from "./components/Toolbar";
import Home from "./pages/Home";
import SettingsPage from "./pages/Settings";
import SupportPage from "./pages/Support";
import Entry from "./pages/Entry";
import { useApp } from "./state/store";
import { useT } from "./lib/strings";
import { useIsDark } from "./lib/theme";
import { Spinner } from "./components/ui";
import logoLight from "./assets/logo-light.webp";
import logoDark from "./assets/logo-dark.webp";

/** Пока служба запускается (несколько секунд после разрешения UAC) — знак
 *  и индикатор, а не пустое окно. */
function Starting() {
  const t = useT();
  const isDark = useIsDark();
  const down = useApp(s => s.daemonDown);
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 16 }}>
      <img src={isDark ? logoDark : logoLight} alt="" draggable={false} style={{ height: 96, objectFit: "contain" }} />
      <Spinner size={20} />
      <div style={{ fontSize: 13, color: "var(--text-secondary)" }}>{down ? t("error.daemon") : t("starting")}</div>
    </div>
  );
}

// Корень окна. Как на Mac, настройки и поддержка — страницы в том же окне, а
// не отдельные окна: панель сверху превращается в «Назад».

export default function App() {
  const t = useT();
  const init = useApp(s => s.init);
  const ready = useApp(s => s.ready);
  const hasSubscription = useApp(s => !!s.settings.subscription_url);
  const page = useApp(s => s.page);
  const setPage = useApp(s => s.setPage);
  const entryOpen = useApp(s => s.entryOpen);

  useEffect(() => { init(); }, [init]);

  let content;
  if (!ready) {
    content = <Starting />;
  } else if (!hasSubscription) {
    content = <Entry />;
  } else if (page === "settings") {
    content = <SettingsPage />;
  } else if (page === "support") {
    content = <SupportPage />;
  } else {
    content = <Home />;
  }

  const back = page === "support" ? () => setPage("settings") : () => setPage("home");
  const title = !hasSubscription ? undefined : page === "settings" ? t("settings.title") : page === "support" ? t("support.title") : undefined;

  return (
    <div className="app-frame" style={{ height: "100%", display: "flex", flexDirection: "column", background: "var(--bg)", position: "relative", overflow: "hidden" }}>
      <Toolbar title={title} onBack={hasSubscription && page !== "home" ? back : undefined} />
      {content}
      {entryOpen && hasSubscription && <Entry asSheet />}
    </div>
  );
}
