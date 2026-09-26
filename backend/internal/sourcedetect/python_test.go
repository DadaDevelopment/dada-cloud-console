package sourcedetect

import (
	"strings"
	"testing"
)

// vishnevkaNames is the member list of the archive a real user uploaded on
// 2026-09-25 [live S3 source-uploads/.../vishnevka]. It is the shape that
// produced a green build over a container that died on
// ModuleNotFoundError: No module named 'aiogram': dependencies split across
// requirements-*.txt, Dockerfiles only as suffixed variants, and the sources
// one directory below the archive's single wrapper directory.
var vishnevkaNames = []string{
	"vishnevka-bot/bot_vk/main.py",
	"vishnevka-bot/bot_vk/keyboards/__init__.py",
	"vishnevka-bot/bot_vk/handlers/__init__.py",
	"vishnevka-bot/bot_vk/__init__.py",
	"vishnevka-bot/requirements-vk.txt",
	"vishnevka-bot/alembic.ini",
	"vishnevka-bot/Dockerfile.migrate",
	"vishnevka-bot/README.md",
	"vishnevka-bot/migrations/script.py.mako",
	"vishnevka-bot/migrations/env.py",
	"vishnevka-bot/migrations/versions/e4ff5367453e_init.py",
	"vishnevka-bot/docker-compose.yml",
	"vishnevka-bot/Dockerfile.vk",
	"vishnevka-bot/bot_tg/main.py",
	"vishnevka-bot/bot_tg/keyboards/__init__.py",
	"vishnevka-bot/bot_tg/middlewares/__init__.py",
	"vishnevka-bot/bot_tg/handlers/__init__.py",
	"vishnevka-bot/bot_tg/__init__.py",
	"vishnevka-bot/requirements-core.txt",
	"vishnevka-bot/Dockerfile.tg",
	"vishnevka-bot/.env.example",
	"vishnevka-bot/requirements-tg.txt",
	"vishnevka-bot/core/config.py",
	"vishnevka-bot/core/__init__.py",
	"vishnevka-bot/core/database.py",
	"vishnevka-bot/core/enums.py",
	"vishnevka-bot/core/models.py",
}

// The live archive must never resolve to a buildable plan: it ships two
// entrypoints (bot_tg/main.py and bot_vk/main.py), so any plan would start one
// of two processes the user packaged. What it must produce instead is an
// honest, actionable hint — which is the whole point of the fix, since the old
// behaviour was a green build over a dead container.
func TestPythonArchivePlanRefusesTwoEntrypoints(t *testing.T) {
	if plan, ok := PythonArchivePlan(vishnevkaNames); ok {
		t.Fatalf("expected refusal for a two-entrypoint archive, got plan %+v", plan)
	}
	hint := UndetectableHint(vishnevkaNames)
	if !strings.Contains(hint, "Dockerfile.migrate") || !strings.Contains(hint, "Dockerfile.tg") {
		t.Fatalf("hint must name the Dockerfile variants the archive actually ships, got %q", hint)
	}
}

// The same project with one bot instead of two is the shape the upload path
// promises to deploy: variant-named requirement files, sources one directory
// down, no manifest any exact-name lookup can find.
func TestPythonArchivePlanSplitRequirements(t *testing.T) {
	names := []string{
		"vishnevka-bot/bot_tg/main.py",
		"vishnevka-bot/bot_tg/__init__.py",
		"vishnevka-bot/core/config.py",
		"vishnevka-bot/requirements-core.txt",
		"vishnevka-bot/requirements-tg.txt",
		"vishnevka-bot/README.md",
	}
	plan, ok := PythonArchivePlan(names)
	if !ok {
		t.Fatal("expected a plan for a single-entrypoint python archive with split requirements")
	}
	if plan.Root != "vishnevka-bot/" {
		t.Fatalf("root = %q, want the wrapper directory stripped", plan.Root)
	}
	if plan.Entrypoint != "bot_tg/main.py" {
		t.Fatalf("entrypoint = %q, want bot_tg/main.py", plan.Entrypoint)
	}
	if len(plan.Requirements) != 2 {
		t.Fatalf("requirements = %v, want both split files", plan.Requirements)
	}

	df := PythonDockerfile(plan)
	for _, req := range plan.Requirements {
		if !strings.Contains(df, "-r "+req) {
			t.Fatalf("Dockerfile must install %s, got:\n%s", req, df)
		}
	}
	if !strings.Contains(df, `CMD ["python", "-u", "bot_tg/main.py"]`) {
		t.Fatalf("Dockerfile must run the detected entrypoint, got:\n%s", df)
	}
}

// requirements.txt sorts first so the generated Dockerfile — and therefore the
// build cache — does not depend on archive member order.
func TestPythonArchivePlanCanonicalRequirementOrder(t *testing.T) {
	names := []string{
		"app/requirements-dev.txt",
		"app/requirements.txt",
		"app/main.py",
	}
	plan, ok := PythonArchivePlan(names)
	if !ok {
		t.Fatal("expected a plan")
	}
	if plan.Requirements[0] != "requirements.txt" {
		t.Fatalf("requirements = %v, want canonical requirements.txt first", plan.Requirements)
	}
}

// A real Dockerfile at the context root is the user's own build instruction and
// Detect resolves it to "docker" long before this runs; refusing here keeps the
// generated wrapper from ever competing with it.
func TestPythonArchivePlanRefusesOwnDockerfile(t *testing.T) {
	names := []string{"app/Dockerfile", "app/main.py", "app/requirements.txt"}
	if _, ok := PythonArchivePlan(names); ok {
		t.Fatal("expected refusal when the archive ships its own Dockerfile")
	}
}

// An archive with dependencies but nothing that looks like an entrypoint has no
// process to start. It must be refused with a hint that names the fix, not
// deployed as a green build.
func TestPythonArchivePlanRefusesNoEntrypoint(t *testing.T) {
	names := []string{"app/requirements.txt", "app/lib/helpers.py"}
	if _, ok := PythonArchivePlan(names); ok {
		t.Fatal("expected refusal when no entrypoint is present")
	}
	if hint := UndetectableHint(names); !strings.Contains(hint, "entrypoint") {
		t.Fatalf("hint must name the missing entrypoint, got %q", hint)
	}
}

// An entrypoint buried deeper than one directory is a package tree whose real
// entrypoint is a module path; starting a file there would run the wrong thing.
func TestPythonArchivePlanRefusesDeepEntrypoint(t *testing.T) {
	names := []string{"app/src/pkg/main.py", "app/requirements.txt"}
	if _, ok := PythonArchivePlan(names); ok {
		t.Fatal("expected refusal for an entrypoint below the depth limit")
	}
}

// A path that cannot be written into a RUN line safely is refused outright,
// because a plan is only worth having when the file it names is the file that
// gets installed.
func TestPythonArchivePlanRefusesUnsafePath(t *testing.T) {
	names := []string{"app/main.py", `app/requirements "evil".txt`}
	if _, ok := PythonArchivePlan(names); ok {
		t.Fatal("expected refusal for an unsafe member name")
	}
}

func TestIsRequirementsFile(t *testing.T) {
	for _, name := range []string{"requirements.txt", "requirements-core.txt", "requirements_dev.txt", "REQUIREMENTS.TXT"} {
		if !isRequirementsFile(name) {
			t.Errorf("%q must count as a requirements file", name)
		}
	}
	for _, name := range []string{"requirementsfoo.txt", "notes.txt", "requirements.md", "main.py"} {
		if isRequirementsFile(name) {
			t.Errorf("%q must not count as a requirements file", name)
		}
	}
}

// The end-to-end contract of the fix: the live archive that produced a green
// build must now reach Detect with a framework of "", so the caller refuses it
// with a verdict instead of queueing a build that cannot work.
func TestDetectLiveVishnevkaArchiveStaysUndetected(t *testing.T) {
	files := make([]zipFile, 0, len(vishnevkaNames))
	for _, name := range vishnevkaNames {
		body := "x"
		if strings.HasSuffix(name, "requirements-core.txt") {
			body = "aiogram==3.4.1\nsqlalchemy\n"
		}
		files = append(files, zipFile{name: name, body: body})
	}
	res, err := Detect(buildZip(t, files))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Framework != "" {
		t.Fatalf("framework = %q, want \"\" for this ambiguous archive", res.Framework)
	}
}
