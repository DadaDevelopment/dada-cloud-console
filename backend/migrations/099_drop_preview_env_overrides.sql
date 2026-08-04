-- 099_drop_preview_env_overrides.sql
--
-- Превью-окружения убраны как фича (см. 9d3b5f5 build-agent, 2b06eba
-- gitops-agent). Единственным читателем preview_env_overrides был
-- copyPreviewEnvVars в gitops-agent — он удалён вместе с созданием превью,
-- так что таблица стала write-only: консоль писала бы в неё значения, которые
-- уже никогда и никем не будут применены.
--
-- На проде в таблице ноль строк на момент удаления, так что данные не теряются.
DROP TABLE IF EXISTS preview_env_overrides;
