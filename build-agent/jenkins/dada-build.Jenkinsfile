// The user-app build job (Jenkins job "ln" / dada-build) is triggered by the
// build-agent runner (internal/worker/runner.go) over buildWithParameters.
//
// The pipeline itself now lives in the jenkins-pipelines shared library as
// vars/dadaBuildPipeline.groovy (so it is versioned and reused, not pasted into
// the job config). The Jenkins job's inline script is just:
//
//     @Library('dada-tuda-jenkins-pipelines@develop') _
//     dadaBuildPipeline()
//
// That pipeline: resolves project/app identity (from params, falling back to a
// DB lookup), clones the repo, and — when the repo ships no Dockerfile —
// templates one from the framework the build-agent detected at build time
// (detected_framework + install_cmd/build_cmd/start_cmd/output_dir/app_port
// parameters). A repo-supplied Dockerfile always wins.
//
// Parameters this job receives (see runner.go execute()): repo, branch,
// framework, buildType, env, project_slug, app_name, detected_framework,
// package_manager, install_cmd, build_cmd, start_cmd, output_dir, app_port.
