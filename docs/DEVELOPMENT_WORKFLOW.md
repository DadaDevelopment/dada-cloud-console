# DADA Cloud Console strict development workflow

## Principle

No feature is considered done until it passes this full loop:

```text
Spec → contract → tests → implementation → local e2e → Git output verification → Helm render → review
```

The project already hit the first classic MVP problem: a bug appeared, and fixing it required manual follow-up prompts. To avoid this, development must move from "ask Claude to build product" to "small controlled slices with acceptance tests".

## Required workflow for every change

### 1. Create a feature ticket

Each ticket must include:

- user story;
- exact input form fields;
- backend API contract;
- expected operation state transitions;
- expected generated Git file path;
- expected generated YAML;
- expected UI state;
- validation rules;
- negative cases.

### 2. Contract first

Before implementation, define:

- request JSON;
- response JSON;
- operation payload schema;
- generated manifest schema;
- status DTO returned to frontend.

### 3. Test before UI polish

For every feature, add:

- backend unit tests for validation;
- backend integration test for operation creation;
- worker test for generated YAML;
- Git writer test using temp repo;
- frontend smoke test for the happy path;
- negative test for invalid input.

### 4. Golden manifests

Every generated Kubernetes manifest must have a golden test.

Example:

```text
tests/golden/servicedatabase/basic.yaml
tests/golden/servicedatabase/with-backup.yaml
tests/golden/serviceendpoint/public-tls.yaml
```

The worker must render the same YAML deterministically.

### 5. Idempotency test

Every mutating operation must be safe to retry.

Test cases:

- worker crashes before commit;
- worker crashes after commit but before DB update;
- same operation is picked twice;
- same resource is created twice by user click;
- Git already contains the desired file.

### 6. Local e2e

Before merge:

```bash
make dev-init
make test
make e2e-create-database
```

The e2e must verify:

- operation row created;
- Git commit created;
- expected YAML exists;
- UI shows operation;
- status eventually reaches Ready in dev simulation.

### 6a. Format gate (once per clone)

```bash
make hooks
```

This points `core.hooksPath` at `.githooks`, enabling the pre-push gate. The gate
runs `gofmt` over the Go files carried by the commits being pushed — the same
check as the Jenkinsfile stage `Go format check`, which runs before any image is
built and inside a `failFast` parallel block. One unformatted file there turns
main red, kills the frontend lane too, and stops every deploy until someone
notices. On 2026-07-31 that cost six consecutive red builds and about eight
hours of blocked delivery.

The hook reads file content from the pushed commit, not the working tree, so a
concurrent session's unstaged edits never block your push. To check or fix the
whole repo at once:

```bash
make fmt-check
make fmt
```

### 7. Helm render gate

Before merge:

```bash
helm lint helm/dada-cloud-console
helm template dada-cloud-console helm/dada-cloud-console --namespace devops-tools
```

### 8. No broad rewrites

Do not ask an agent to "build the full product" again.

Use slices:

- CreateServiceDatabase validation
- Git writer idempotency
- Operation timeline
- Project RBAC
- ServiceEndpoint create
- Status aggregation

## Definition of Done

A change is done only when:

- API contract exists;
- DB migration exists if needed;
- backend tests pass;
- frontend build passes;
- golden manifest test exists;
- generated YAML path is verified;
- operation state machine is tested;
- audit event is created;
- Helm chart renders;
- README/runbook is updated.

## Prompt discipline for Claude/Codex

Use this template:

```text
Implement only <feature>.
Do not change unrelated files.
Before coding, list touched files.
Add tests first:
- backend validation test
- worker golden manifest test
- API integration test
Then implement.
Run:
- go test ./...
- npm run build
- helm template
Return:
- changed files
- tests run
- risks
```
