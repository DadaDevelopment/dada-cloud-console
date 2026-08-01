import type { Messages } from "./common";

/** Boxes page — ephemeral root sandboxes an agent works in (boxes.*). */
export const boxes: Messages = {
  "boxes.title": { ru: "Боксы", en: "Boxes" },
  "boxes.subtitle": {
    ru: "Одноразовая машина с root, готовая за секунды. Ваш агент подключается к ней напрямую.",
    en: "A disposable root machine, ready in seconds. Your own agent connects to it directly.",
  },
  "boxes.create": { ru: "Поднять бокс", en: "Bring up a box" },
  "boxes.creating": { ru: "Поднимаем бокс…", en: "Bringing the box up…" },
  "boxes.creatingHint": {
    ru: "Ответ придёт только когда внутри бокса реально выполнится команда. Это до двух минут, если в пуле нет тёплого тела.",
    en: "The answer comes back only once a command has actually run inside the box. Up to two minutes if the pool has no warm body.",
  },

  "boxes.error.load": { ru: "Не удалось загрузить боксы", en: "Failed to load boxes" },

  "boxes.empty.title": { ru: "Пока нет боксов", en: "No boxes yet" },
  "boxes.empty.description": {
    ru: "Бокс — одноразовая машина с root, которую агент получает целиком. Ставьте что угодно, ломайте что угодно: она живёт ровно столько, сколько нужно.",
    en: "A box is a disposable root machine an agent gets all of. Install anything, break anything: it lives exactly as long as you need it.",
  },
  "boxes.empty.cta": { ru: "Поднять первый бокс →", en: "Bring up your first box →" },
  "boxes.empty.step1": { ru: "Поднимите бокс — одна кнопка, одно тело", en: "Bring up a box — one button, one body" },
  "boxes.empty.step2": {
    ru: "Скопируйте mcpServers-сниппет в конфиг своего агента",
    en: "Paste the mcpServers snippet into your agent's config",
  },
  "boxes.empty.step3": {
    ru: "Агент работает в боксе напрямую — не через нас",
    en: "Your agent works in the box directly — not through us",
  },

  "boxes.col.status": { ru: "Статус", en: "Status" },
  "boxes.col.image": { ru: "Образ", en: "Image" },
  "boxes.col.expires": { ru: "Уснёт", en: "Sleeps" },
  "boxes.expired": { ru: "срок вышел", en: "past its TTL" },

  "boxes.action.connect": { ru: "Подключение", en: "Connect" },
  "boxes.action.suspend": { ru: "Усыпить", en: "Suspend" },
  "boxes.action.resume": { ru: "Разбудить", en: "Resume" },
  "boxes.action.delete": { ru: "Удалить", en: "Delete" },
  "boxes.action.deleteConfirm": {
    ru: "Удалить бокс {name}? Диск и всё, что на нём, пропадёт.",
    en: "Delete box {name}? Its disk and everything on it goes with it.",
  },

  "boxes.modal.title": { ru: "Поднять бокс", en: "Bring up a box" },
  "boxes.modal.name": { ru: "Имя", en: "Name" },
  "boxes.modal.nameHint": { ru: "необязательно — сгенерируем сами", en: "optional — one is generated for you" },
  "boxes.modal.ttl": { ru: "Уснёт через", en: "Sleeps after" },
  "boxes.modal.ttl1h": { ru: "1 час", en: "1 hour" },
  "boxes.modal.ttl4h": { ru: "4 часа", en: "4 hours" },
  "boxes.modal.ttl12h": { ru: "12 часов", en: "12 hours" },
  "boxes.modal.ttlNote": {
    ru: "По TTL бокс засыпает, а не удаляется: диск переживает сон, работа не теряется.",
    en: "At its TTL the box goes to sleep rather than being deleted: the disk survives, the work is not lost.",
  },
  "boxes.modal.submit": { ru: "Поднять", en: "Bring it up" },

  "boxes.connect.title": { ru: "Бокс готов", en: "Your box is up" },
  "boxes.connect.readyIn": { ru: "Готов за {ms} мс ({pool})", en: "Ready in {ms} ms ({pool})" },
  "boxes.connect.ssh": { ru: "SSH", en: "SSH" },
  "boxes.connect.mcp": { ru: "Конфиг MCP для агента", en: "MCP config for your agent" },
  "boxes.connect.mcpUnavailable": {
    ru: "У этого бокса нет собственной двери — команды пойдут через control plane. Это деградированный бокс, а не продукт.",
    en: "This box has no door of its own — commands would pass through the control plane. That is a degraded box, not the product.",
  },
  "boxes.connect.token": { ru: "Одноразовый токен сессии", en: "One-time session token" },
  "boxes.connect.tokenWarning": {
    ru: "Показывается ровно один раз. Мы храним только его sha256 — восстановить нельзя, можно только выпустить новый.",
    en: "Shown exactly once. Only its sha256 is stored — it cannot be recovered, only replaced with a new one.",
  },
  "boxes.connect.newSession": { ru: "Выпустить новый токен", en: "Mint a new token" },
  "boxes.connect.copy": { ru: "Копировать", en: "Copy" },
  "boxes.connect.copied": { ru: "Скопировано", en: "Copied" },
  "boxes.connect.close": { ru: "Закрыть", en: "Close" },
};
