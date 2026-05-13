import { useEffect, useRef } from "react";
import { useVPNStore } from "../store/vpnStore";

export function useStatusPoller(intervalMs = 2000) {
  const fetchStatus = useVPNStore((s) => s.fetchStatus);
  const state = useVPNStore((s) => s.status.state);
  const timerRef = useRef<ReturnType<typeof setInterval>>();

  useEffect(() => {
    // Poll faster when connecting/disconnecting
    const interval =
      state === "connecting" || state === "disconnecting" ? 500 : intervalMs;

    timerRef.current = setInterval(fetchStatus, interval);
    return () => clearInterval(timerRef.current);
  }, [state, intervalMs]);
}
