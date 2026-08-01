import type { Messages } from "./common";

/** App volume file manager — browse/edit an app's persistent disk (apps.files.*). */
export const files: Messages = {
  "apps.files.title": { ru: "Файлы", en: "Files" },
  "apps.files.crumb": { ru: "Файлы", en: "Files" },
  "apps.files.subtitle": {
    ru: "Постоянный диск приложения — читается напрямую из работающего пода",
    en: "The app's persistent disk, read live from a running pod",
  },
  "apps.files.open": { ru: "Открыть файлы", en: "Browse files" },

  "apps.files.root": { ru: "Корень тома", en: "Volume root" },
  "apps.files.refresh": { ru: "Обновить", en: "Refresh" },
  "apps.files.newFolder": { ru: "Новая папка", en: "New folder" },
  "apps.files.upload": { ru: "Загрузить", en: "Upload" },
  "apps.files.downloadDir": { ru: "Скачать .tar.gz", en: "Download .tar.gz" },
  "apps.files.download": { ru: "Скачать", en: "Download" },
  "apps.files.rename": { ru: "Переименовать", en: "Rename" },
  "apps.files.delete": { ru: "Удалить", en: "Delete" },
  "apps.files.cancel": { ru: "Отмена", en: "Cancel" },
  "apps.files.create": { ru: "Создать", en: "Create" },
  "apps.files.save": { ru: "Сохранить", en: "Save" },
  "apps.files.saving": { ru: "Сохраняю…", en: "Saving…" },
  "apps.files.close": { ru: "Закрыть", en: "Close" },
  "apps.files.reload": { ru: "Перечитать", en: "Reload" },

  "apps.files.column.name": { ru: "Имя", en: "Name" },
  "apps.files.column.size": { ru: "Размер", en: "Size" },
  "apps.files.column.modified": { ru: "Изменён", en: "Modified" },

  "apps.files.empty": { ru: "Папка пуста", en: "This folder is empty" },
  "apps.files.emptyHint": {
    ru: "Перетащите файлы сюда или нажмите «Загрузить»",
    en: "Drop files here, or use Upload",
  },
  "apps.files.dropHere": { ru: "Отпустите — загрузим сюда", en: "Drop to upload here" },
  "apps.files.truncated": {
    ru: "Показаны первые {count} записей — в папке их больше",
    en: "Showing the first {count} entries — the folder has more",
  },
  "apps.files.searchPlaceholder": { ru: "Фильтр по имени", en: "Filter by name" },
  "apps.files.noMatches": { ru: "Ничего не найдено", en: "Nothing matches" },

  "apps.files.uploading": { ru: "Загрузка {name} — {percent}%", en: "Uploading {name} — {percent}%" },
  "apps.files.uploaded": { ru: "Загружено: {name}", en: "Uploaded {name}" },
  "apps.files.saved": { ru: "Сохранено", en: "Saved" },
  "apps.files.created": { ru: "Папка создана", en: "Folder created" },
  "apps.files.renamed": { ru: "Переименовано", en: "Renamed" },
  "apps.files.deleted": { ru: "Удалено: {name}", en: "Deleted {name}" },

  "apps.files.newFolder.title": { ru: "Новая папка", en: "New folder" },
  "apps.files.newFolder.label": { ru: "Имя папки", en: "Folder name" },
  "apps.files.rename.title": { ru: "Переименовать", en: "Rename" },
  "apps.files.rename.label": { ru: "Новое имя", en: "New name" },
  "apps.files.delete.title": { ru: "Удалить безвозвратно?", en: "Delete permanently?" },
  "apps.files.delete.body": {
    ru: "«{name}» будет удалён с диска приложения. Отменить нельзя — восстановление только из снапшота тома.",
    en: "“{name}” will be removed from the app's disk. This cannot be undone — the only recovery is a volume snapshot.",
  },
  "apps.files.delete.dirBody": {
    ru: "Папка «{name}» и всё её содержимое будут удалены. Отменить нельзя.",
    en: "The folder “{name}” and everything inside it will be removed. This cannot be undone.",
  },
  "apps.files.delete.confirm": { ru: "Удалить", en: "Delete" },

  "apps.files.editor.unsaved": { ru: "Есть несохранённые правки", en: "Unsaved changes" },
  "apps.files.editor.hint": { ru: "Cmd/Ctrl+S — сохранить", en: "Cmd/Ctrl+S to save" },
  "apps.files.editor.readonly": {
    ru: "Только чтение — нужна роль с правом записи",
    en: "Read-only — write access required",
  },
  "apps.files.editor.binary": {
    ru: "Двоичный файл — открыть в редакторе нельзя, скачайте его",
    en: "Binary file — it cannot be edited here, download it instead",
  },
  "apps.files.editor.tooLarge": {
    ru: "Файл больше 1 МиБ — открыть в редакторе нельзя, скачайте его",
    en: "The file is larger than 1 MiB — download it instead",
  },
  "apps.files.editor.conflict": {
    ru: "Файл изменился на диске после того, как вы его открыли. Перечитайте, иначе перезапишете чужие правки.",
    en: "The file changed on disk after you opened it. Reload it, or you will overwrite someone else's edit.",
  },
  "apps.files.editor.overwrite": { ru: "Всё равно сохранить", en: "Save anyway" },
  "apps.files.preview.image": { ru: "Предпросмотр", en: "Preview" },

  "apps.files.error.noVolume": {
    ru: "У приложения нет постоянного диска. Подключите его в настройках хранилища.",
    en: "This app has no persistent disk. Attach one in the storage settings.",
  },
  "apps.files.error.noPod": {
    ru: "Нет работающего пода — файлы читаются из живого контейнера. Запустите приложение и попробуйте снова.",
    en: "No running pod — files are read from the live container. Start the app and try again.",
  },
  "apps.files.error.noShell": {
    ru: "В образе приложения нет shell, поэтому файлы не открыть отсюда.",
    en: "The app's image has no shell, so its files cannot be browsed from here.",
  },
  "apps.files.error.generic": { ru: "Не удалось получить файлы", en: "Failed to load files" },
  "apps.files.error.uploadTooLarge": { ru: "Файл больше 100 МиБ", en: "The file is larger than 100 MiB" },
  "apps.files.error.noPermission": {
    ru: "Недостаточно прав для просмотра файлов приложения",
    en: "You do not have permission to browse this app's files",
  },
  "apps.files.settingsLink": { ru: "Настройки хранилища", en: "Storage settings" },
};
