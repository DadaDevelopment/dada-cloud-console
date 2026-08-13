-- 117_git_repos_start_command.sql
-- Start command the console can hand an app that otherwise crashloops forever
-- with no way to say how it should be launched (diagnosed as
-- cause_kind=app_needs_args, with no lever in the product to fix it).
--
-- This REPLACES the image's start command; it does not append arguments to it.
-- The Jenkins shared library templates images with a CMD and no ENTRYPOINT
-- (see jenkins-pipelines vars/dadaBuildPipeline.groovy), so an append-only
-- args field would overwrite CMD and leave the kubelet trying to exec a flag
-- as a binary. The chart therefore renders command: ["sh","-c"] with this
-- string as the single argument.
--
-- Free text, shell-style, interpreted by the shell rather than split here --
-- git_repos already carries every other single-valued app knob this way
-- (profile, worker, framework_override), never as an array column, so this
-- follows the same house style instead of introducing text[].
--
-- Nullable with no default: NULL means unset, and every reader (the console
-- API, the gitops-agent renderer) must treat unset as "omit the key" so the
-- 100+ apps that never touch this field keep rendering byte-identical
-- values.yaml.
--
-- Forward-only, idempotent.

ALTER TABLE git_repos
    ADD COLUMN IF NOT EXISTS start_command TEXT;

COMMENT ON COLUMN git_repos.start_command IS
    'Start command that replaces the image CMD, run through sh -c (shell-style free text). NULL = unset; the renderer must omit the values.yaml startCommand key entirely, not emit startCommand: "".';
