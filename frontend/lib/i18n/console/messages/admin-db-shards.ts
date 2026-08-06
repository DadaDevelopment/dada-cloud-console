import type { Messages } from "./common";

/**
 * Admin — per-shard database consumption ("who is eating this instance").
 */
export const adminDBShards: Messages = {
  "adminDbShards.crumb.dbShards": { ru: "Базы", en: "Databases" },
  "adminDbShards.title": { ru: "Кто ест инстанс", en: "Who is eating the instance" },
  "adminDbShards.subtitle": {
    ru: "Те же замеры, что владелец видит на странице своей базы, но сгруппированные по инстансу. Ничего не опрашивается вживую.",
    en: "The same samples the owner sees on their own database page, grouped by instance. Nothing is queried live.",
  },
  "adminDbShards.accessDenied": {
    ru: "Нет доступа. Раздел доступен администраторам и аналитикам платформы.",
    en: "No access. This page is for platform admins and analysts.",
  },
  "adminDbShards.error.load": { ru: "Не удалось загрузить данные по шардам", en: "Failed to load shard data" },
  "adminDbShards.empty": {
    ru: "Замеров ещё нет: сборщик не настроен или не отработал ни разу.",
    en: "No samples yet: the collector is not configured or has not run.",
  },
  "adminDbShards.state.open": { ru: "открыт", en: "open" },
  "adminDbShards.state.closed": { ru: "закрыт", en: "closed" },
  "adminDbShards.state.draining": { ru: "выводится", en: "draining" },
  "adminDbShards.state.unregistered": { ru: "нет в реестре", en: "not in registry" },
  "adminDbShards.platform": { ru: "платформенный", en: "platform" },
  "adminDbShards.stat.sampled": { ru: "Замеряно", en: "Sampled" },
  "adminDbShards.stat.capacity": { ru: "Ёмкость", en: "Capacity" },
  "adminDbShards.stat.databases": { ru: "Баз", en: "Databases" },
  "adminDbShards.stat.uptime": { ru: "Инстанс живёт", en: "Instance up" },
  "adminDbShards.stat.collected": { ru: "Замер", en: "Sampled at" },
  "adminDbShards.capacity.unbounded": { ru: "не ограничена", en: "unbounded" },
  "adminDbShards.col.database": { ru: "База", en: "Database" },
  "adminDbShards.col.owner": { ru: "Владелец", en: "Owner" },
  "adminDbShards.col.size": { ru: "Размер", en: "Size" },
  "adminDbShards.col.share": { ru: "Доля", en: "Share" },
  "adminDbShards.col.growth": { ru: "Рост", en: "Growth" },
  "adminDbShards.col.conns": { ru: "Соединений", en: "Connections" },
  "adminDbShards.col.advisories": { ru: "Находки", en: "Findings" },
  "adminDbShards.orphan": { ru: "без владельца", en: "unowned" },
  "adminDbShards.orphan.hint": {
    ru: "База есть на инстансе, но контрол-плейн о ней не знает. Именно так платформенный postgres оказался на одном томе с чужим растущим проектом.",
    en: "The database is on the instance but the control plane does not know it. This is how platform postgres ended up sharing a volume with an unwatched tenant.",
  },
  "adminDbShards.noSamples": { ru: "Замеров по этому шарду нет", en: "No samples for this shard" },
  "adminDbShards.window": { ru: "рост за {days} дн.", en: "growth over {days}d" },
};
