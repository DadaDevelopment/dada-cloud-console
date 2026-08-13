package cliapp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/cli/internal/agentmarker"
	"github.com/dada-tuda/console/cli/internal/apiclient"
	"github.com/dada-tuda/console/cli/internal/appname"
	"github.com/dada-tuda/console/cli/internal/archive"
	"github.com/dada-tuda/console/cli/internal/gitremote"
	"github.com/dada-tuda/console/cli/internal/ui"
)

// DeployOptions are the resolved inputs to a single `ddc deploy` run. Upload
// skips git entirely and ships the folder as an archive.
type DeployOptions struct {
	Dir     string
	AppName string
	Project string
	Upload  bool
}

// buildPoll* control how often ddc asks the console for build status while
// streaming progress. 2s balances "feels live" against hammering the API.
const (
	buildPollInterval = 2 * time.Second
	buildPollTimeout  = 20 * time.Minute
	urlPollInterval   = 3 * time.Second
	urlPollTimeout    = 3 * time.Minute
)

// accessWait* bound the wait for a user's trip to GitHub. Five minutes covers
// signing in on a phone and picking a repository; past that the deploy stops
// waiting and ships the folder as an archive rather than hanging.
const (
	accessPollInterval = 2 * time.Second
	accessWaitTimeout  = 5 * time.Minute
)

// stagePercent maps each step of a deploy to a share of the progress bar.
// The numbers are ordered milestones, not a time estimate: the bar only moves
// when the platform actually reports the next state, so it never advertises
// progress that has not happened.
var stagePercent = map[string]int{
	"login":     4,
	"target":    8,
	"link":      12,
	"packaging": 16,
	"uploading": 26,
	"queued":    34,
	"detecting": 46,
	"building":  66,
	"pushing":   86,
	"url":       94,
}

// buildStageLabel names the console's build statuses in the words a user
// recognises. An unknown status keeps its own name rather than being hidden.
var buildStageLabel = map[string]string{
	"queued":    "Сборка в очереди",
	"detecting": "Определяем фреймворк",
	"building":  "Собираем образ",
	"pushing":   "Публикуем образ",
}

// Deploy runs the full v0 flow: resolve project/environment/app name, build
// the app from its git remote or from an uploaded archive, follow the build,
// then print the live URL once the platform has assigned one.
func Deploy(ctx context.Context, cfg Config, opts DeployOptions, in io.Reader, out io.Writer) error {
	if err := EnsureLoggedIn(ctx, cfg, out); err != nil {
		return err
	}
	prog := ui.New(out)
	defer prog.Stop()

	client := apiclient.New(cfg.APIBase, &http.Client{}, TokenSource(cfg), agentmarker.DetectFromEnv())

	appName, err := resolveAppName(opts)
	if err != nil {
		return err
	}
	prog.Stage("Ищем проект", stagePercent["target"])

	target, err := resolveTarget(ctx, client, opts, appName, in, prog, out)
	if err != nil {
		return err
	}
	projectID, envID := target.ProjectID, target.EnvID
	prog.Info("Приложение %s → %s / %s", appName, target.ProjectName, target.EnvName)

	buildID, fromGit := "", false
	if !opts.Upload {
		buildID, fromGit = deployFromGit(ctx, client, projectID, envID, appName, opts, in, prog)
	}
	if !fromGit {
		buildID, err = deployFromArchive(ctx, client, projectID, envID, appName, opts.Dir, prog)
		if err != nil {
			return err
		}
	}

	final, err := streamBuildStatus(ctx, client, projectID, envID, appName, buildID, prog)
	if err != nil {
		return err
	}
	if final.Status != apiclient.StatusSuccess {
		prog.Stop()
		return fmt.Errorf("%s\n%s", explainBuildFailure(final), buildConsoleURL(cfg, projectID, appName, final.ID))
	}

	prog.Stage("Ждём адрес приложения", stagePercent["url"])
	url, ok, err := pollAppURL(ctx, client, projectID, envID, appName)
	if err != nil {
		return err
	}
	if ok {
		prog.Success("Готово: %s", url)
	} else {
		prog.Success("Сборка прошла. Адрес ещё не назначен - он появится в консоли.")
	}
	return nil
}

// deployFromGit builds straight from the folder's GitHub remote, which is the
// main road: the console gets a real repository, a real commit and an
// auto-deploy hook for every later push. A dirty working tree is not a reason
// to leave that road - the platform simply builds what origin already has, and
// the CLI says so. The archive upload stays as the answer for folders that are
// not a usable GitHub repo at all, and every drop to it is announced.
func deployFromGit(ctx context.Context, client *apiclient.Client, projectID, envID, appName string, opts DeployOptions, in io.Reader, prog *ui.Progress) (string, bool) {
	dir := opts.Dir
	info := gitremote.Detect(dir)
	if !info.IsRepo {
		return "", false
	}
	fallback := func(reason string) (string, bool) {
		prog.Info("Заливаем папку вместо сборки из git: %s.", reason)
		return "", false
	}
	if info.Unsupported != "" {
		return fallback(info.Unsupported)
	}
	if !gitremote.BranchOnOrigin(dir, info.Branch) {
		return fallback("ветки " + info.Branch + " ещё нет в origin - запушьте её, и деплой пойдёт из git")
	}
	if !gitremote.OriginHasSource(dir, info.Branch, info.SubdirOfRoot) {
		return fallback("в origin/" + info.Branch + " лежит только документация - код ещё не закоммичен, " +
			"платформа собирала бы пустой репозиторий; сделайте git add/commit/push, и деплой пойдёт из git")
	}

	prog.Stage("Проверяем репозиторий", stagePercent["link"])
	repos, err := client.ListGitRepos(ctx, projectID, envID)
	if err != nil {
		return fallback("не удалось прочитать репозитории окружения (" + apiclient.Explain(err) + ")")
	}
	linked, found := findGitRepo(repos, appName)
	if found && isUploadPlaceholder(linked, appName) {
		prog.Stage("Переключаем на "+info.FullName, stagePercent["link"])
		if err := client.DisconnectGitRepo(ctx, projectID, envID, linked.ID); err != nil {
			return fallback("не удалось отвязать прошлую загрузку архива (" + apiclient.Explain(err) + ")")
		}
		found = false
	}
	switch {
	case found && linked.RepoFullName != info.FullName:
		return fallback(fmt.Sprintf("приложение %q уже связано с %s", appName, linked.RepoFullName))
	case !found:
		prog.Stage("Подключаем "+info.FullName, stagePercent["link"])
		created, err := client.ConnectGitRepo(ctx, projectID, envID, apiclient.ConnectGitRepoRequest{
			RepoFullName:     info.FullName,
			AppName:          appName,
			ProductionBranch: info.Branch,
			RootDir:          rootDirOrDot(info.SubdirOfRoot),
			AutoDeploy:       true,
			Provider:         "github",
		})
		if isGithubAccessRequired(err) {
			created, err = connectAfterGitHubInstall(ctx, client, projectID, envID, appName, info, in, prog)
		}
		if err != nil && !isAlreadyConnected(err) {
			if isGithubAccessRequired(err) {
				return fallback("платформа не может склонировать репозиторий - установите GitHub App на " + info.FullName)
			}
			return fallback("подключить репозиторий не вышло (" + apiclient.Explain(err) + ")")
		}
		linked = created
	}

	build, err := client.TriggerBuild(ctx, projectID, envID, appName)
	if err != nil {
		return fallback("запустить сборку из git не вышло (" + apiclient.Explain(err) + ")")
	}
	prog.Info("Источник: github.com/%s @ %s", info.FullName, info.Branch)
	prog.Info("%s", autoDeployNote(linked, info.Branch))
	if info.Dirty || !info.HeadPushed {
		prog.Info("Собираем то, что лежит в origin/%s - локальные правки уедут следующим push.", info.Branch)
	}
	prog.Stage("Сборка в очереди", stagePercent["queued"])
	return build.ID, true
}

// isGithubAccessRequired reports the one connect failure the user can still
// fix without leaving the deploy: the repository is private (or otherwise not
// clonable anonymously) and the project has no GitHub App installation bound
// to it (backend/internal/api/gitrepos.go linkGitRepo).
func isGithubAccessRequired(err error) bool {
	var apiErr *apiclient.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "github_access_required"
}

// connectAfterGitHubInstall turns the dead end of a private repository into a
// two-minute detour instead of "connect GitHub in the console", which meant
// abandoning the deploy and losing the git path (and with it auto-deploy) to an
// archive upload.
//
// It tries the two doors in the order that actually opens them.
//
// First authorization. Installations are recorded per org
// (backend/migrations/026), so a user who already installed the App from
// another org - the common case, and the one a user reads as "но оно же уже
// стоит" - has a live installation the platform refuses to reuse. Authorizing
// proves they own that GitHub account and imports those installations into this
// project's org (backend/internal/api/git_oauth.go). Sending them to install
// instead is a dead end twice over: GitHub shows an already-installed account a
// configure page and never calls the setup URL back, so nothing is recorded.
//
// Then installation, for the user who genuinely has no installation yet: there
// authorization returns an empty list and only the install URL helps.
//
// A non-interactive run (no terminal behind in) gets EOF from the first prompt
// and falls straight back to the archive, so scripts and CI never hang here.
func connectAfterGitHubInstall(ctx context.Context, client *apiclient.Client, projectID, envID, appName string, info gitremote.Info, in io.Reader, prog *ui.Progress) (apiclient.GitRepo, error) {
	if !interactive(in) {
		return apiclient.GitRepo{}, fmt.Errorf("доступ к %s не подтверждён", info.FullName)
	}
	owner, _, _ := strings.Cut(info.FullName, "/")
	connect := func() (apiclient.GitRepo, error) {
		prog.Stage("Подключаем "+info.FullName, stagePercent["link"])
		return client.ConnectGitRepo(ctx, projectID, envID, apiclient.ConnectGitRepoRequest{
			RepoFullName:     info.FullName,
			AppName:          appName,
			ProductionBranch: info.Branch,
			RootDir:          rootDirOrDot(info.SubdirOfRoot),
			AutoDeploy:       true,
			Provider:         "github",
		})
	}

	lastErr := fmt.Errorf("доступ к %s не подтверждён", info.FullName)
	authorizeURL, authErr := client.GitHubAuthorizeURL(ctx, projectID)
	if authErr == nil {
		visit(authorizeURL, prog,
			fmt.Sprintf("Репозиторий %s закрыт для платформы - откройте и нажмите Authorize:", info.FullName),
			"Дальше ничего нажимать не надо - деплой продолжится сам.")
		if !awaitAccess(ctx, client, projectID, owner, prog) {
			return apiclient.GitRepo{}, lastErr
		}
		repo, err := connect()
		if !isGithubAccessRequired(err) {
			return repo, err
		}
		lastErr = err
	}

	installURL, err := client.GitInstallURL(ctx, projectID)
	if err != nil {
		return apiclient.GitRepo{}, fmt.Errorf("ссылку на установку GitHub App получить не вышло (%s)", apiclient.Explain(err))
	}
	visit(installURL, prog,
		fmt.Sprintf("Платформа всё ещё не видит %s - установите на него GitHub App:", info.FullName),
		fmt.Sprintf("Выберите %s и нажмите Install - деплой продолжится сам.", owner))
	if !awaitAccess(ctx, client, projectID, owner, prog) {
		return apiclient.GitRepo{}, lastErr
	}
	return connect()
}

// visit prints a URL and opens it. Printing comes first and is never skipped:
// the browser may be the wrong one, headless, or on another machine, and the
// URL is the only thing that still works then.
func visit(url string, prog *ui.Progress, headline, action string) {
	prog.Info("%s", headline)
	prog.Info("  %s", url)
	prog.Info("%s", action)
	openBrowser(url)
}

// awaitAccess waits for the trip to GitHub to land, watching the console
// instead of asking the user to confirm it. Both callbacks (install and
// authorization) write an installation row for the project's org before they
// redirect the browser, so the row appearing for this repository's owner IS
// the completion signal - a keypress would only be the user's guess at it.
func awaitAccess(ctx context.Context, client *apiclient.Client, projectID, owner string, prog *ui.Progress) bool {
	prog.Stage("Ждём доступ к github.com/"+owner, stagePercent["link"])
	deadline := time.Now().Add(accessWaitTimeout)
	for {
		installations, err := client.ListAvailableInstallations(ctx, projectID)
		if err == nil {
			for _, inst := range installations {
				if strings.EqualFold(inst.AccountLogin, owner) {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(accessPollInterval):
		}
	}
}

// interactive reports whether there is a person behind in. Without one the
// GitHub detour is pointless - nobody will click anything - so the deploy must
// not spend accessWaitTimeout finding that out.
var interactive = func(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// isUploadPlaceholder reports whether an app's link is the row an archive
// upload leaves behind: provider "archive" with the synthetic name
// "upload/<app>" (backend/internal/api/uploadsource.go). Such a row is not a
// user's deliberate choice of source, it is the residue of one earlier
// `ddc deploy` that fell back to the archive path. Treating it like a real
// link locked the app onto uploads forever: every later deploy read
// "приложение уже связано с upload/<app>" and fell back again, so a repo that
// was on GitHub the whole time could never reach the git path.
func isUploadPlaceholder(repo apiclient.GitRepo, appName string) bool {
	return repo.Provider == "archive" || repo.RepoFullName == "upload/"+appName
}

// autoDeployNote says whether later pushes will deploy by themselves. The
// build-agent only enqueues a webhook build when auto_deploy is on and the
// push matches the production branch (build-agent/internal/server/server.go),
// and those deliveries come from the GitHub App -- an anonymously linked
// repository never sends them, so it is told the truth instead of a promise.
func autoDeployNote(repo apiclient.GitRepo, branch string) string {
	if !repo.AutoDeploy {
		return "Авто-деплой выключен: следующий push собирать вручную (ddc deploy)."
	}
	if repo.PlatformAccess == "anonymous" {
		return "Авто-деплой включён, но хук придёт только после установки GitHub App на репозиторий - пока запускайте ddc deploy."
	}
	return fmt.Sprintf("Авто-деплой включён: следующий push в %s соберётся сам.", branch)
}

func rootDirOrDot(sub string) string {
	if sub == "" {
		return "."
	}
	return sub
}

func findGitRepo(repos []apiclient.GitRepo, appName string) (apiclient.GitRepo, bool) {
	for _, r := range repos {
		if r.AppName == appName {
			return r, true
		}
	}
	return apiclient.GitRepo{}, false
}

// isAlreadyConnected reports the harmless half of the link conflict: this app
// is already linked to this very repository, which is exactly the state the
// caller wanted.
func isAlreadyConnected(err error) bool {
	var apiErr *apiclient.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "repo_already_connected"
}

// deployFromArchive is the no-git path: package the directory, upload it, and
// return the id of the build the upload queued.
func deployFromArchive(ctx context.Context, client *apiclient.Client, projectID, envID, appName, dir string, prog *ui.Progress) (string, error) {
	prog.Stage("Собираем архив", stagePercent["packaging"])
	entries, total, err := archive.Plan(dir)
	if err != nil {
		return "", fmt.Errorf("чтение %s: %w", dir, err)
	}
	if total > archive.MaxBytes {
		return "", fmt.Errorf("проект весит %.1fМБ, лимит загрузки консоли %dМБ - "+
			"уберите крупные файлы или добавьте их в .gitignore",
			float64(total)/1024/1024, archive.MaxBytes/1024/1024)
	}
	prog.Stage(fmt.Sprintf("Собираем архив: %d файлов, %.1fМБ", len(entries), float64(total)/1024/1024), stagePercent["packaging"])

	data, err := archive.Build(dir, entries)
	if err != nil {
		return "", fmt.Errorf("сборка архива: %w", err)
	}

	prog.Stage("Загружаем архив", stagePercent["uploading"])
	uploadResp, err := client.UploadSourceArchive(ctx, projectID, envID, appName, appName+".tar.gz", data)
	if err != nil {
		return "", fmt.Errorf("загрузка не прошла: %s", apiclient.Explain(err))
	}
	prog.Info("%s", describeDetection(uploadResp.Detected.Framework, uploadResp.Detected.Port))
	prog.Stage("Сборка в очереди", stagePercent["queued"])
	return uploadResp.Build.ID, nil
}

// describeDetection says what the upload-time scan found without overstating
// it. An empty framework is not a dead end: the console passes "auto" and the
// pipeline detects again on the unpacked archive
// (build-agent/internal/detect/detect.go), so the honest line is "the builder
// will decide", not "not detected (port 0)", which reads like a failure.
func describeDetection(framework string, port int) string {
	if framework == "" {
		return "Манифест не найден - фреймворк определит сборщик"
	}
	if port <= 0 {
		return fmt.Sprintf("Фреймворк: %s (порт назначит платформа)", framework)
	}
	return fmt.Sprintf("Фреймворк: %s (порт %d)", framework, port)
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// resolveTarget decides where this directory deploys to. The first run in a
// folder never shows a menu: it reuses the project named after the folder, or
// creates it. Later runs reuse what was remembered for that folder. The menu
// survives only as the fallback for --project naming something ambiguous.
func resolveTarget(ctx context.Context, client *apiclient.Client, opts DeployOptions, appName string, in io.Reader, prog *ui.Progress, out io.Writer) (Target, error) {
	if opts.Project == "" {
		if remembered, ok, err := LookupTarget(opts.Dir); err == nil && ok {
			return remembered, nil
		}
	}

	projects, err := client.ListProjects(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("список проектов: %s", apiclient.Explain(err))
	}

	wanted := opts.Project
	if wanted == "" {
		wanted = appName
	}

	project, found := findProject(projects, wanted)
	if !found {
		if opts.Project != "" {
			return Target{}, fmt.Errorf("проекта %q нет - запустите без --project, чтобы создать новый", opts.Project)
		}
		project, err = createProjectForFolder(ctx, client, wanted, prog)
		if err != nil {
			return Target{}, err
		}
	}

	env, err := resolveEnvironment(ctx, client, project, in, prog, out)
	if err != nil {
		return Target{}, err
	}

	target := Target{
		ProjectID:   project.ID,
		ProjectName: nonEmpty(project.DisplayName, project.Name),
		EnvID:       env.ID,
		EnvName:     env.Name,
		AppName:     appName,
	}
	if err := RememberTarget(opts.Dir, target); err != nil {
		prog.Info("не удалось запомнить цель для этой папки (%v)", err)
	}
	return target, nil
}

// createProjectForFolder mints the project this folder deploys into. Slugs
// are unique platform-wide (backend/internal/api/projects.go:218), so a name
// another tenant already took comes back 409 and is retried with a short
// suffix rather than dumping the user back into a menu.
func createProjectForFolder(ctx context.Context, client *apiclient.Client, wanted string, prog *ui.Progress) (apiclient.Project, error) {
	slug := appname.NormalizeProject(wanted)
	if !appname.ValidProject(slug) {
		return apiclient.Project{}, fmt.Errorf("не вышло вывести имя проекта из %q - передайте существующий через --project", wanted)
	}

	prog.Stage("Создаём проект "+slug, stagePercent["target"])
	project, err := client.CreateProject(ctx, slug)
	if err == nil {
		prog.Info("Первый деплой из этой папки - создан проект %s", slug)
		return project, nil
	}

	var apiErr *apiclient.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		return apiclient.Project{}, fmt.Errorf("создание проекта %q: %s", slug, apiclient.Explain(err))
	}

	taken := slug
	suffixed, sufErr := suffixSlug(slug)
	if sufErr != nil {
		return apiclient.Project{}, sufErr
	}
	project, err = client.CreateProject(ctx, suffixed)
	if err != nil {
		return apiclient.Project{}, fmt.Errorf("создание проекта %q: %s", suffixed, apiclient.Explain(err))
	}
	prog.Info("Имя %s занято на платформе - создан проект %s", taken, suffixed)
	return project, nil
}

// suffixSlug appends a random 6-hex-digit suffix, trimming the base so the
// result still fits the console's 40-character project slug limit.
func suffixSlug(slug string) (string, error) {
	var raw [3]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	suffix := "-" + hex.EncodeToString(raw[:])
	base := slug
	if len(base)+len(suffix) > 40 {
		base = strings.Trim(base[:40-len(suffix)], "-")
	}
	return base + suffix, nil
}

// findProject matches by exact name first, then by display name, so both
// `--project my-api` and `--project "My API"` land on the same row.
func findProject(projects []apiclient.Project, wanted string) (apiclient.Project, bool) {
	for _, p := range projects {
		if p.Name == wanted {
			return p, true
		}
	}
	for _, p := range projects {
		if p.DisplayName == wanted {
			return p, true
		}
	}
	return apiclient.Project{}, false
}

// resolveEnvironment picks the environment to deploy into: the one actually
// named prod when the project has it, the only one when there is only one, a
// prod-typed one after that, and a prompt only when nothing decides.
func resolveEnvironment(ctx context.Context, client *apiclient.Client, project apiclient.Project, in io.Reader, prog *ui.Progress, out io.Writer) (apiclient.Environment, error) {
	envs, err := client.GetProjectEnvironments(ctx, project.ID)
	if err != nil {
		return apiclient.Environment{}, fmt.Errorf("список окружений: %s", apiclient.Explain(err))
	}
	if len(envs) == 0 {
		return apiclient.Environment{}, fmt.Errorf("в проекте %q нет окружений", project.Name)
	}
	if len(envs) == 1 {
		return envs[0], nil
	}
	for _, e := range envs {
		if e.Name == "prod" || e.Name == "production" {
			return e, nil
		}
	}
	for _, e := range envs {
		if e.Type == "prod" {
			return e, nil
		}
	}
	prog.Pause()
	defer prog.Resume()
	return choose(in, out, "Выберите окружение", envs, func(e apiclient.Environment) string {
		return fmt.Sprintf("%s (%s)", e.Name, e.Type)
	})
}

// choose prints a numbered list of items and reads a 1-based selection from
// in. It is generic over T so it serves both the project and environment
// prompts with the same code.
func choose[T any](in io.Reader, out io.Writer, prompt string, items []T, label func(T) string) (T, error) {
	var zero T
	fmt.Fprintln(out, prompt+":")
	for i, item := range items {
		fmt.Fprintf(out, "  %d) %s\n", i+1, label(item))
	}
	fmt.Fprint(out, "> ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return zero, fmt.Errorf("ввод не получен")
	}
	choice := strings.TrimSpace(scanner.Text())
	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(items) {
		return zero, fmt.Errorf("непонятный выбор %q", choice)
	}
	return items[n-1], nil
}

// resolveAppName uses opts.AppName if the caller passed --name, otherwise
// derives it from the directory name.
func resolveAppName(opts DeployOptions) (string, error) {
	if opts.AppName != "" {
		if err := appname.Validate(opts.AppName); err != nil {
			return "", err
		}
		return opts.AppName, nil
	}

	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	normalized := appname.Normalize(base)
	if err := appname.Validate(normalized); err != nil {
		return "", fmt.Errorf("не вышло вывести имя приложения из папки %q - передайте его через --name", base)
	}
	return normalized, nil
}

// streamBuildStatus polls the build's status until it reaches a terminal
// state, moving the progress bar on every change, and returns the terminal
// status.
func streamBuildStatus(ctx context.Context, client *apiclient.Client, projectID, envID, appName, buildID string, prog *ui.Progress) (apiclient.Build, error) {
	deadline := time.Now().Add(buildPollTimeout)

	for {
		if time.Now().After(deadline) {
			return apiclient.Build{}, fmt.Errorf("сборка не завершилась за %s", buildPollTimeout)
		}

		build, ok, err := client.LatestBuild(ctx, projectID, envID, appName)
		if err != nil {
			return apiclient.Build{}, fmt.Errorf("статус сборки: %s", apiclient.Explain(err))
		}
		if ok && build.ID == buildID {
			if apiclient.IsTerminalBuildStatus(build.Status) {
				return build, nil
			}
			label, known := buildStageLabel[build.Status]
			if !known {
				label = build.Status
			}
			prog.Stage(label, stagePercent[build.Status])
		}

		select {
		case <-ctx.Done():
			return apiclient.Build{}, ctx.Err()
		case <-time.After(buildPollInterval):
		}
	}
}

// failReasonText translates the build agent's fail_reason vocabulary
// (build-agent/internal/worker/runner.go) into the next step a user can take.
// The platform's own failures say so out loud: a build that died before the
// pipeline read any code must never read as "your code is broken".
var failReasonText = map[string]string{
	"no_dockerfile":           "сборщик не понял, как собрать проект - добавьте Dockerfile или манифест зависимостей",
	"dockerfile_build_failed": "образ не собрался - ошибка внутри сборки",
	"git_auth_failed":         "платформа не смогла прочитать репозиторий - переподключите его",
	"platform_error":          "упало на нашей стороне, не в вашем коде - можно просто повторить",
	"build_failed":            "сборка упала",
}

// explainBuildFailure turns a terminal build into one line the user can act
// on: the classified reason, then the console line that caused it, with the
// machine code stripped from the front of the detail (the agent stores
// "<fail_reason>: <detail>", and printing both shows the same fact twice).
func explainBuildFailure(b apiclient.Build) string {
	reason := ""
	if b.FailReason != nil {
		reason = *b.FailReason
	}
	headline, known := failReasonText[reason]
	if !known {
		headline = fmt.Sprintf("сборка завершилась статусом %q", b.Status)
	}

	detail := ""
	if b.ErrorMessage != nil {
		detail = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(*b.ErrorMessage), reason+": "))
	}
	if detail == "" {
		return headline
	}
	return headline + "\n" + detail
}

// buildConsoleURL points at the build's page in the web console, derived from
// the API base the CLI is already configured with.
func buildConsoleURL(cfg Config, projectID, appName, buildID string) string {
	base := strings.TrimSuffix(cfg.APIBase, "/api/v1")
	return fmt.Sprintf("%s/projects/%s/apps/%s/builds/%s", base, projectID, appName, buildID)
}

// pollAppURL waits briefly for the platform to assign the app a live URL
// after a successful build - it is created by the same handoff, but not
// necessarily synced into the snapshot the instant the build finishes.
func pollAppURL(ctx context.Context, client *apiclient.Client, projectID, envID, appName string) (string, bool, error) {
	deadline := time.Now().Add(urlPollTimeout)
	for {
		url, ok, err := client.FindAppURL(ctx, projectID, envID, appName)
		if err != nil {
			return "", false, fmt.Errorf("адрес приложения: %s", apiclient.Explain(err))
		}
		if ok {
			return url, true, nil
		}
		if time.Now().After(deadline) {
			return "", false, nil
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(urlPollInterval):
		}
	}
}
