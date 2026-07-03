import type { Messages } from "./common";

/** App Servers list page + detail page. Namespace: appServers.* */
export const appServers: Messages = {
  "appServers.title": { ru: "Серверы приложений", en: "App Servers" },
  "appServers.subtitle": { ru: "Выделенные VM-хосты для Docker Compose рабочих нагрузок.", en: "Dedicated VM hosts for Docker Compose workloads." },
  "appServers.create": { ru: "Создать AppServer", en: "Create AppServer" },

  "appServers.col.name": { ru: "Название", en: "Name" },
  "appServers.col.status": { ru: "Статус", en: "Status" },
  "appServers.col.vmIp": { ru: "IP виртуальной машины", en: "VM IP" },
  "appServers.col.portainer": { ru: "Portainer", en: "Portainer" },
  "appServers.col.actions": { ru: "Действия", en: "Actions" },

  "appServers.updated": { ru: "Обновлено {ago}", en: "Updated {ago}" },

  "appServers.heartbeat.online": { ru: "Онлайн (Portainer heartbeat)", en: "Online (Portainer heartbeat)" },
  "appServers.heartbeat.none": { ru: "Нет heartbeat", en: "No heartbeat" },

  "appServers.search": { ru: "Поиск серверов…", en: "Search app servers…" },

  "appServers.empty.title": { ru: "Серверов пока нет", en: "No AppServers yet" },
  "appServers.empty.provision": { ru: "Создать первый VM-хост →", en: "Provision the first VM host →" },

  "appServers.modal.title": { ru: "Создать AppServer", en: "Create AppServer" },

  "appServers.field.name.label": { ru: "Название", en: "Name" },
  "appServers.field.name.help": { ru: "Имя в нижнем регистре в стиле DNS — используется для VM и Portainer endpoint.", en: "Lowercase DNS-style name used for the VM and Portainer endpoint." },

  "appServers.field.source.label": { ru: "Источник", en: "Source" },
  "appServers.field.source.terraform": { ru: "Создать (Terraform)", en: "Provision (Terraform)" },
  "appServers.field.source.manual": { ru: "Подключить существующую VM", en: "Connect existing VM" },
  "appServers.field.source.help.terraform": { ru: "Мы создадим и настроим новую VM для вас.", en: "We create and bootstrap a new VM for you." },
  "appServers.field.source.help.manual": { ru: "Подключите VM, которой вы уже владеете. Мы зайдём по SSH один раз, чтобы установить Docker и edge-агент.", en: "Connect a VM you already own. We SSH in once to install Docker + the edge agent." },

  "appServers.field.flavor.label": { ru: "Конфигурация", en: "Flavor" },
  "appServers.field.region.label": { ru: "Регион", en: "Region" },
  "appServers.field.osImage.label": { ru: "Образ ОС", en: "OS image" },
  "appServers.field.sshKeyName.label": { ru: "Имя SSH-ключа", en: "SSH key name" },

  "appServers.field.vmIp.label": { ru: "IP-адрес VM", en: "VM IP address" },
  "appServers.field.sshPort.label": { ru: "SSH-порт", en: "SSH port" },
  "appServers.field.sshUser.label": { ru: "SSH-пользователь", en: "SSH user" },
  "appServers.field.sshKey.label": { ru: "SSH приватный ключ", en: "SSH private key" },
  "appServers.field.sshKey.warn": { ru: "Используется однократно для установки edge-агента, затем удаляется — никогда не сохраняется.", en: "Used once to install the edge agent, then discarded — never stored." },

  "appServers.error.load": { ru: "Не удалось загрузить серверы приложений", en: "Failed to load app servers" },
  "appServers.error.create": { ru: "Не удалось создать сервер приложений", en: "Failed to create app server" },
  "appServers.error.delete": { ru: "Не удалось удалить сервер приложений", en: "Failed to delete app server" },
  "appServers.error.notFound": { ru: "Сервер приложений не найден", en: "App server not found" },
  "appServers.error.loadDetail": { ru: "Не удалось загрузить сервер приложений", en: "Failed to load app server" },

  "appServers.detail.status": { ru: "Статус", en: "Status" },
  "appServers.detail.heartbeat": { ru: "Heartbeat", en: "Heartbeat" },
  "appServers.detail.vmIp": { ru: "IP виртуальной машины", en: "VM IP" },
  "appServers.detail.portainer": { ru: "Portainer", en: "Portainer" },
  "appServers.detail.online": { ru: "онлайн", en: "online" },
  "appServers.detail.offline": { ru: "офлайн", en: "offline" },

  "appServers.retry.button": { ru: "Повторить подключение", en: "Retry connect" },
  "appServers.retry.title": { ru: "Повторить подключение AppServer", en: "Retry AppServer connect" },
  "appServers.retry.submit": { ru: "Повторить", en: "Retry" },
  "appServers.retry.help": { ru: "Подключение завершилось ошибкой. Вставьте SSH-ключ ещё раз, чтобы повторить — он используется однократно и не сохраняется.", en: "The connect failed. Paste the SSH key again to retry — it is used once and not stored." },
  "appServers.error.retry": { ru: "Не удалось повторить подключение", en: "Failed to retry connect" },

  "appServers.discover.button": { ru: "Discovery нагрузки", en: "Discover workload" },
  "appServers.discover.title": { ru: "Обнаруженная нагрузка", en: "Discovered workload" },
  "appServers.discover.readonly": { ru: "только чтение · через Portainer, без SSH", en: "read-only · via Portainer, no SSH" },
  "appServers.discover.failed": { ru: "Не удалось выполнить discovery нагрузки", en: "Workload discovery failed" },
  "appServers.discover.timeout": { ru: "Discovery не завершился вовремя", en: "Discovery did not finish in time" },
  "appServers.discover.col.container": { ru: "Контейнер", en: "Container" },
  "appServers.discover.col.image": { ru: "Образ", en: "Image" },
  "appServers.discover.col.ports": { ru: "Порты", en: "Ports" },
  "appServers.discover.col.volumes": { ru: "Тома", en: "Volumes" },
  "appServers.discover.externalVolumes": { ru: "Блок external-томов для GitOps compose", en: "External-volume block for the GitOps compose" },
};
