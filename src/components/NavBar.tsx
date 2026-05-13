import { NavLink } from "react-router-dom";
import { Shield, Settings } from "lucide-react";
import { useVPNStore } from "../store/vpnStore";

const tabs = [
  { to: "/", icon: Shield, label: "Главная" },
  { to: "/settings", icon: Settings, label: "Настройки" },
];

export default function NavBar() {
  const state = useVPNStore(s => s.status.state);

  return (
    <nav className="shrink-0 px-8 py-2" style={{ borderTop: "1px solid var(--c-nav-border)", transition: "border-color 0.3s" }}>
      <div className="flex justify-around">
        {tabs.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            className="flex flex-col items-center gap-0.5 px-8 py-1.5 rounded-xl transition-all duration-200"
            style={({ isActive }) => ({
              color: isActive ? "#60a5fa" : "var(--c-text-sub)",
            })}
          >
            {({ isActive }) => (
              <>
                <div className="relative">
                  <Icon size={21} strokeWidth={isActive ? 2.5 : 1.5} />
                  {to === "/" && state === "connected" && (
                    <span className="absolute -top-0.5 -right-0.5 w-2 h-2 bg-green-400 rounded-full border border-surface-900" />
                  )}
                </div>
                <span className="text-[10px] font-medium">{label}</span>
              </>
            )}
          </NavLink>
        ))}
      </div>
    </nav>
  );
}
