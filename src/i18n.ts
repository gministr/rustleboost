import { useVPNStore } from "./store/vpnStore";

export type Language = "ru" | "en";

/**
 * Every user-facing string lives here.
 *
 * The two dictionaries share one key type, so TypeScript refuses to compile
 * a key that exists in one language but not the other — a missing translation
 * cannot reach the user as a blank label or a stray Russian word in the
 * English UI.
 */
const ru = {
  // Connection states
  stateDisconnected: "Не подключено",
  stateConnecting: "Подключение...",
  stateConnected: "Защищено",
  stateDisconnecting: "Отключение...",

  // Main screen
  servers: "Серверы",
  noServers: "Нет серверов",
  refreshSubscription: "Обновить подписку",
  checkPing: "Проверить пинг",
  unlimited: "бессрочно",
  expired: "Подписка истекла",
  daysLeft: "Осталось дней",

  // Navigation
  navHome: "Главная",
  navSettings: "Настройки",

  // Errors
  errConnect: "Не удалось подключиться",
  errDisconnect: "Не удалось отключиться",
  errSubscription: "Не удалось обновить подписку",
  errSettings: "Не удалось сохранить настройки",

  // Updates
  updateAvailable: "Доступно обновление",
  updateAction: "Обновить",
  updateDownloading: "Загрузка...",
  updateDone: "Готово!",

  // Settings — sections
  secSubscription: "Подписка",
  secDevice: "Устройство (HWID)",
  secRouting: "Маршрутизация",
  secConnection: "Подключение",
  secUpdates: "Обновления",
  secInterface: "Интерфейс",

  // Settings — subscription
  subscriptionKey: "Ключ подписки",
  subscriptionPlaceholder: "Вставьте ссылку подписки",
  save: "Сохранить",
  saving: "Сохраняем...",
  saved: "Сохранено",
  loading: "Загрузка...",

  // Settings — device
  deviceHint: "Идентификатор этого компьютера. Пригодится, если потребуется отвязать устройство.",
  copied: "Скопировано",

  // Settings — routing
  routingMode: "Режим маршрутизации трафика",
  routeAll: "Весь трафик через VPN",
  routeAllSub: "Все сайты и приложения идут через сервер",
  routeRu: "RU-сайты напрямую",
  routeRuSub: ".ru .рф .ру — прямое соединение, VPN для остальных",
  routeCn: "CN-сайты напрямую",
  routeCnSub: ".cn .com.cn — прямое соединение, VPN для остальных",
  appliesNextConnect: "Применяется при следующем подключении",

  // Settings — router (which core carries proxy traffic)
  secRouter: "Ядро подключения",
  routerHint: "Какой сайт откроется, а какой — нет, зависит не от сервера, а от того, что блокирует именно ваша сеть. Если что-то не подключается, попробуйте другое ядро.",
  routerAuto: "Авто",
  routerAutoSub: "sing-box, где можно, иначе Xray — меньше процессов",
  routerSingBox: "Только sing-box",
  routerSingBoxSub: "Серверы на XHTTP не подключатся в этом режиме",
  routerXray: "Только Xray",
  routerXraySub: "Все серверы идут через Xray",

  // Main screen — shown when "connected" could not be verified
  goToSettings: "Настройки",

  // Settings — connection
  tunMode: "TUN Mode",
  tunModeSub: "Системный VPN-интерфейс RustleBoost (все приложения)",
  killSwitch: "Kill Switch",
  killSwitchSub: "Блокировать трафик, если VPN неожиданно оборвётся",
  autoConnect: "Автоподключение",
  autoConnectSub: "Запускать приложение с Windows и подключаться сразу",

  // Settings — updates
  autoUpdateSubscription: "Автообновление подписки",
  interval: "Интервал",
  appVersion: "Версия приложения",
  checkUpdates: "Проверить",
  checking: "Проверка...",
  upToDate: "Установлена последняя версия",
  updateFailed: "Не удалось проверить обновления",
  downloadingVersion: "Загружаем",
  restarting: "Готово, перезапускаем...",

  // Settings — interface
  language: "Язык",

  // Onboarding
  onboardingTitle: "Введите ключ подписки, чтобы начать использование",
  subscriptionUrl: "URL подписки",
  loadingSubscription: "Загрузка подписки...",
  connectSubscription: "Подключить подписку",
  errLoadSubscription: "Не удалось загрузить подписку. Проверьте ссылку.",

  // Title bar
  themeLight: "Светлая тема",
  themeDark: "Тёмная тема",
} as const;

type Dictionary = Record<keyof typeof ru, string>;

const en: Dictionary = {
  stateDisconnected: "Not connected",
  stateConnecting: "Connecting...",
  stateConnected: "Protected",
  stateDisconnecting: "Disconnecting...",

  servers: "Servers",
  noServers: "No servers",
  refreshSubscription: "Refresh subscription",
  checkPing: "Test latency",
  unlimited: "never expires",
  expired: "Subscription expired",
  daysLeft: "Days left",

  navHome: "Home",
  navSettings: "Settings",

  errConnect: "Could not connect",
  errDisconnect: "Could not disconnect",
  errSubscription: "Could not refresh the subscription",
  errSettings: "Could not save settings",

  updateAvailable: "Update available",
  updateAction: "Update",
  updateDownloading: "Downloading...",
  updateDone: "Done!",

  secSubscription: "Subscription",
  secDevice: "Device (HWID)",
  secRouting: "Routing",
  secConnection: "Connection",
  secUpdates: "Updates",
  secInterface: "Interface",

  subscriptionKey: "Subscription key",
  subscriptionPlaceholder: "Paste your subscription link",
  save: "Save",
  saving: "Saving...",
  saved: "Saved",
  loading: "Loading...",

  deviceHint: "This computer's identifier. Useful if you ever need to unlink the device.",
  copied: "Copied",

  routingMode: "Traffic routing mode",
  routeAll: "All traffic through VPN",
  routeAllSub: "Every site and app goes through the server",
  routeRu: "RU sites direct",
  routeRuSub: ".ru .рф .ру connect directly, VPN for everything else",
  routeCn: "CN sites direct",
  routeCnSub: ".cn .com.cn connect directly, VPN for everything else",
  appliesNextConnect: "Applies on the next connection",

  secRouter: "Connection core",
  routerHint: "Which site loads and which doesn't depends on what your specific network blocks, not the server. If something won't connect, try the other core.",
  routerAuto: "Auto",
  routerAutoSub: "sing-box where it can, Xray otherwise — fewer processes",
  routerSingBox: "sing-box only",
  routerSingBoxSub: "Servers using XHTTP won't connect in this mode",
  routerXray: "Xray only",
  routerXraySub: "Every server routes through Xray",

  goToSettings: "Settings",

  tunMode: "TUN Mode",
  tunModeSub: "System-wide RustleBoost VPN interface (all apps)",
  killSwitch: "Kill Switch",
  killSwitchSub: "Block traffic if the VPN drops unexpectedly",
  autoConnect: "Auto-connect",
  autoConnectSub: "Start with Windows and connect right away",

  autoUpdateSubscription: "Auto-refresh subscription",
  interval: "Interval",
  appVersion: "App version",
  checkUpdates: "Check",
  checking: "Checking...",
  upToDate: "You are on the latest version",
  updateFailed: "Could not check for updates",
  downloadingVersion: "Downloading",
  restarting: "Done, restarting...",

  language: "Language",

  onboardingTitle: "Enter your subscription key to get started",
  subscriptionUrl: "Subscription URL",
  loadingSubscription: "Loading subscription...",
  connectSubscription: "Connect subscription",
  errLoadSubscription: "Could not load the subscription. Check the link.",

  themeLight: "Light theme",
  themeDark: "Dark theme",
};

const dictionaries: Record<Language, Dictionary> = { ru, en };

export type TranslationKey = keyof Dictionary;

export function translate(language: Language, key: TranslationKey): string {
  return (dictionaries[language] ?? dictionaries.ru)[key];
}

/**
 * Returns a lookup bound to the language in settings. Reading the language
 * from the store means a change in Settings re-renders every screen at once,
 * with no reload and no stale labels.
 */
export function useT() {
  const language = useVPNStore(s => s.settings.language) as Language;
  return (key: TranslationKey) => translate(language, key);
}

/** Hours are formatted per language: "12ч" vs "12h". */
export function formatHours(language: Language, hours: number): string {
  return language === "ru" ? `${hours}ч` : `${hours}h`;
}
