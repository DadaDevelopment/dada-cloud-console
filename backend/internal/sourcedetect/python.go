package sourcedetect

import (
	"fmt"
	"sort"
	"strings"
)

// pythonEntrypointNames are the module names a script-style Python project uses
// for the process that is meant to run. They are the same names
// dadaBuildPipeline's python start step falls back to, so an archive that
// deploys here and an archive that deploys through the git path pick the same
// file.
var pythonEntrypointNames = []string{"main.py", "bot.py", "app.py", "run.py"}

// pythonPlanMaxDepth is how far below the build context an entrypoint may sit.
// A bot packaged as "bot_tg/main.py" is the common shape; deeper than that the
// archive describes a package tree whose real entrypoint is a module path, not
// a file, and guessing one would start the wrong process.
const pythonPlanMaxDepth = 1

// PythonPlan describes how to build an uploaded Python project whose shape no
// manifest names: which prefix is the build context, which requirement files
// to install, and which file to run.
type PythonPlan struct {
	Root         string
	Requirements []string
	Entrypoint   string
}

// PythonArchivePlan resolves a buildable plan for a Python upload that carries
// no manifest detection can name.
//
// It exists because "requirements.txt" is only one of the names a real project
// uses for its dependencies. A bot that splits them into requirements-core.txt
// and requirements-tg.txt, and keeps its modules one directory down, matched
// none of the detection branches: not findManifest (which compares the exact
// base name), not rootLevelPythonSources (no .py at the context root), not
// singleDirPythonSources (the .py files sit below the single content
// directory, not directly in it), and not StaticSiteRoot (a .py disqualifies
// it). Detection answered with an empty framework.
//
// An empty framework used to fail the build loudly. It no longer does: the
// pipeline re-sniffs the unpacked sources, finds a .py, renders its python
// template, and that template installs dependencies only from a file literally
// named requirements.txt or pyproject.toml. So a variant-named requirement
// file is never installed, the image builds clean, the build is reported
// success, and the container dies on first import. On 2026-09-25 that is
// exactly what the platform told artem4212@bk.ru: a green build and a created
// app, 84 seconds after upload, followed 72 seconds later by
// ModuleNotFoundError: No module named 'aiogram' [live psql builds
// 92153601-542b-4646-9dd2-750b59ee6f21 + app_health_alerts].
//
// Returning a plan here lets the control plane write a Dockerfile that
// installs every requirement file the archive actually ships, which is the
// only signal that survives the trip to the builder (see InjectDockerfile).
//
// Ambiguity is refused rather than guessed, for the same reason StaticSiteRoot
// refuses two sibling sites: an archive with two entrypoints (a Telegram bot
// beside a VK bot) describes two processes, and starting one of them would
// deploy half an upload under the name of the whole.
func PythonArchivePlan(names []string) (PythonPlan, bool) {
	root := detectRootFromNames(names)

	var reqs, entrypoints []string
	hasPython := false
	for _, name := range names {
		if isToolingPath(name) {
			continue
		}
		rel := strings.TrimPrefix(name, root)
		if rel == "" || (rel == name && root != "") {
			continue
		}
		if !isSafeContextPath(rel) {
			return PythonPlan{}, false
		}
		base := rel
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
			return PythonPlan{}, false
		}
		if strings.HasSuffix(strings.ToLower(base), ".py") {
			hasPython = true
		}
		if isRequirementsFile(base) {
			reqs = append(reqs, rel)
		}
		if pathDepth(rel) <= pythonPlanMaxDepth && isPythonEntrypointName(base) {
			entrypoints = append(entrypoints, rel)
		}
	}

	if !hasPython || len(entrypoints) != 1 {
		return PythonPlan{}, false
	}
	return PythonPlan{Root: root, Requirements: sortRequirements(reqs), Entrypoint: entrypoints[0]}, true
}

// isSafeContextPath reports whether an archive member's name is safe to write
// into a generated Dockerfile. Anything else is refused outright: a quote or a
// space in a path would change what the RUN line means, and a plan is only
// worth having when the file it names is the file that gets installed.
func isSafeContextPath(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") {
		return false
	}
	for _, r := range rel {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '/':
		default:
			return false
		}
	}
	return !strings.Contains(rel, "..")
}

// isRequirementsFile reports whether a base name is a pip requirements file
// under any of the names projects actually use: requirements.txt itself, and
// the split variants (requirements-core.txt, requirements_dev.txt).
func isRequirementsFile(base string) bool {
	lower := strings.ToLower(base)
	if !strings.HasSuffix(lower, ".txt") || !strings.HasPrefix(lower, "requirements") {
		return false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(lower, "requirements"), ".txt")
	return rest == "" || strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "_")
}

func isPythonEntrypointName(base string) bool {
	for _, name := range pythonEntrypointNames {
		if strings.EqualFold(base, name) {
			return true
		}
	}
	return false
}

func pathDepth(rel string) int {
	return strings.Count(rel, "/")
}

// sortRequirements puts the canonical requirements.txt first and orders the
// rest, so the generated Dockerfile is byte-identical for byte-identical input
// and its layer cache is not invalidated by archive member order.
func sortRequirements(reqs []string) []string {
	if len(reqs) == 0 {
		return nil
	}
	out := append([]string(nil), reqs...)
	sort.Slice(out, func(i, j int) bool {
		bi, bj := strings.ToLower(out[i]), strings.ToLower(out[j])
		pi, pj := bi == "requirements.txt", bj == "requirements.txt"
		if pi != pj {
			return pi
		}
		return bi < bj
	})
	return out
}

// PythonDockerfile renders the Dockerfile the platform writes on behalf of a
// Python upload whose dependencies detection can see but the pipeline's own
// template cannot install.
//
// The install step is one RUN over every requirement file the archive ships,
// so a project that split its dependencies gets all of them. It is deliberately
// not tolerant of a failed install: an image that builds while pip fails is the
// whole defect this replaces, so a broken requirement file must fail the build
// and reach the user as a build error rather than as a crash loop minutes later.
func PythonDockerfile(plan PythonPlan) string {
	var b strings.Builder
	b.WriteString("FROM python:3.12-slim\n")
	b.WriteString("ENV PYTHONUNBUFFERED=1 PIP_DISABLE_PIP_VERSION_CHECK=1\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("COPY . .\n")
	if len(plan.Requirements) > 0 {
		b.WriteString("RUN pip install --no-cache-dir")
		for _, req := range plan.Requirements {
			fmt.Fprintf(&b, " -r %s", req)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "CMD [\"python\", \"-u\", \"%s\"]\n", plan.Entrypoint)
	return b.String()
}

// UndetectableHint explains, in one actionable sentence, why an archive could
// not be built, for the user who is holding the archive.
//
// It exists because the platform's previous answer to an undetectable upload
// was a green build over a dead container: the user was told the deploy
// succeeded and then had to work out from a crash loop that nothing had been
// installed. Naming the specific obstacle is the minimum honest verdict, and
// each branch names something the user can act on in one step.
func UndetectableHint(names []string) string {
	root := detectRootFromNames(names)

	var dockerVariants, entrypoints []string
	hasPython, hasRequirements := false, false
	for _, name := range names {
		if isToolingPath(name) {
			continue
		}
		rel := strings.TrimPrefix(name, root)
		if rel == "" || (rel == name && root != "") {
			continue
		}
		base := rel
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if pathDepth(rel) == 0 && strings.HasPrefix(base, "Dockerfile.") {
			dockerVariants = append(dockerVariants, base)
		}
		if strings.HasSuffix(strings.ToLower(base), ".py") {
			hasPython = true
		}
		if isRequirementsFile(base) {
			hasRequirements = true
		}
		if pathDepth(rel) <= pythonPlanMaxDepth && isPythonEntrypointName(base) {
			entrypoints = append(entrypoints, rel)
		}
	}

	if len(dockerVariants) > 0 {
		sort.Strings(dockerVariants)
		return fmt.Sprintf("archive ships %s but no plain Dockerfile: rename the one you want deployed to Dockerfile and upload again", strings.Join(dockerVariants, ", "))
	}
	if len(entrypoints) > 1 {
		sort.Strings(entrypoints)
		return fmt.Sprintf("archive ships several entrypoints (%s): upload one app per deploy, or add a Dockerfile naming the one to run", strings.Join(entrypoints, ", "))
	}
	if hasPython && !hasRequirements && len(entrypoints) == 0 {
		return fmt.Sprintf("python sources found but no entrypoint: name it one of %s at the top level, or add a Dockerfile", strings.Join(pythonEntrypointNames, ", "))
	}
	if hasRequirements && len(entrypoints) == 0 {
		return fmt.Sprintf("requirements found but no entrypoint: name the file that starts your app one of %s, or add a Dockerfile", strings.Join(pythonEntrypointNames, ", "))
	}
	return "no framework could be detected: add a Dockerfile, or a manifest (package.json, requirements.txt, pyproject.toml, go.mod, pom.xml) at the top level of the archive"
}
