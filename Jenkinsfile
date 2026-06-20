def GO_VERSION   = '1.25'
def NODE_VERSION = '20'

def GO_BUILDER_IMAGE   = "golang:${GO_VERSION}-alpine"
def NODE_BUILDER_IMAGE = "node:${NODE_VERSION}-bookworm"
// docker:24, not docker:29 — docker:29-dind's embedded BuildKit "session"
// healthcheck fatally kills dockerd on heavy builds ("only one connection
// allowed" → dind SIGTERM → pod OOM/abort). docker:24 is the in-repo-proven-good
// dind (see jenkins-pipelines kubePodTemplate commit 01d47ea).
def DOCKER_CLI_IMAGE   = 'docker:24-cli'
def DOCKER_DIND_IMAGE  = 'docker:24-dind'

def GITHUB_REGISTRY      = 'ghcr.io'
def GITHUB_ORG           = 'dadadevelopment'
def BACKEND_IMAGE         = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-backend"
def FRONTEND_IMAGE        = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-frontend"
def GITOPS_AGENT_IMAGE    = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-gitops-agent"
def PORTAINER_AGENT_IMAGE = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-portainer-agent"

// GitOps write-back: after a successful push, pin the just-built tag into the
// ArgoCD source so prod actually rolls (there is NO image-updater; the tag is
// the deploy trigger). Argo's prod app tracks the console-migration branch of
// argo-infra; only the 4 console component tags are touched (migrationJob is
// left alone).
def ARGO_REPO        = 'github.com/DadaDevelopment/argo-infra.git'
def ARGO_BRANCH      = 'console-migration'
def ARGO_VALUES_PATH = 'clusters/beget-prod/projects/platform/environments/prod/apps/cloud-console/values.yaml'

// Frontend OIDC build-time constants. NEXT_PUBLIC_* vars are baked into the
// JS bundle — must be set during both `npm run build` and `docker build`.
def NEXT_PUBLIC_AUTH_MODE       = 'oidc'
def NEXT_PUBLIC_KEYCLOAK_ISSUER = 'https://id.dada-tuda.ru/realms/master'
def NEXT_PUBLIC_OIDC_CLIENT_ID  = 'dada-console'

def podLabel  = "kubeagent-${env.JOB_BASE_NAME ?: 'job'}-${env.BUILD_NUMBER ?: 'manual'}"
        .replaceAll('[^A-Za-z0-9-]', '-')
        .toLowerCase()
def agentName = "kubeagent-${env.JOB_BASE_NAME}-${env.BUILD_NUMBER}-${UUID.randomUUID().toString().take(6)}"

properties([disableConcurrentBuilds(abortPrevious: true)])

podTemplate(
        cloud: 'self-managed',
        label: podLabel,
        namespace: 'devops-tools',
        serviceAccount: 'jenkins-admin',
        yaml: """
apiVersion: v1
kind: Pod
metadata:
  name: ${agentName}
  annotations:
    # ArgoCD (app jenkins-beget, syncPolicy.automated.prune+selfHeal=true) prunes
    # this ephemeral agent pod on its ~180s reconcile tick — every build pod was
    # deleted mid-run at the :42s reconcile phase (#146/#147 "Pod just failed",
    # ChannelClosedException), regardless of build stage. Dropping the
    # tracking-id alone did NOT stop it (ArgoCD still prunes extraneous resources
    # in the managed namespace). These opt-outs tell ArgoCD to ignore the pod and
    # never prune it.
    argocd.argoproj.io/compare-options: IgnoreExtraneous
    argocd.argoproj.io/sync-options: Prune=false
  labels:
    app.kubernetes.io/name: jenkins-agent
    app.kubernetes.io/part-of: dada-cloud-console
    app.kubernetes.io/managed-by: jenkins
spec:
  # Override the namespace default activeDeadlineSeconds. devops-tools caps pod
  # lifetime at ~360s: EVERY agent pod was killed exactly ~360s after creation
  # (#146 pods 359s/360s apart; #148 pod 362s), which looked like a periodic
  # external pruner but is a per-pod deadline. The build needs longer than 6 min
  # (tests + frontend build + 4 image builds + push), so it never finished.
  # 1 hour is plenty and well under any sane runaway cap.
  activeDeadlineSeconds: 3600
  priorityClassName: critical
  securityContext:
    fsGroup: 1000
  volumes:
    - name: workspace-volume
      emptyDir:
        sizeLimit: 3Gi
    - name: go-cache
      emptyDir:
        sizeLimit: 2Gi
    - name: go-mod-cache
      emptyDir:
        sizeLimit: 2Gi
    - name: npm-cache
      emptyDir:
        sizeLimit: 1Gi
    - name: docker-graph-storage
      emptyDir:
        sizeLimit: 8Gi
    - name: docker-certs
      emptyDir: {}
    - name: tools-volume
      emptyDir: {}
  containers:
    - name: jnlp
      # Digest-pinned (was floating :latest). A floating tag silently changes the
      # remoting version, which mid-build mismatches the 2.559-jdk21 controller and
      # drops the JNLP4 channel (ChannelClosedException, "removed or offline" —
      # observed #138/#139). This exact image handshakes cleanly against the
      # controller. Bump deliberately. Matches jenkins-pipelines kubePodTemplate.
      image: jenkins/inbound-agent@sha256:65b16b388a32be41d95134a5be7ce58e20fbc4d862e12f021ae64bf83bd5fe7d
      tty: true
      workingDir: /home/jenkins/agent
      resources:
        requests:
          # cpu 500m (was 50m): the JNLP4 channel drops deterministically during
          # the CPU-heavy 2nd-image go build (#143/#144: gitops-agent build ->
          # ChannelClosedException -> pod fails). go-builder+dind peg the node;
          # at a 300m cap the agent is starved and misses its heartbeat, so the
          # controller declares it offline. Reserving real CPU keeps the agent
          # responsive enough to heartbeat through the build.
          cpu: "500m"
          memory: "256Mi"
        limits:
          cpu: "2000m"
          memory: "512Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: tools-volume
          mountPath: /tools

    - name: go-builder
      image: ${GO_BUILDER_IMAGE}
      command: ['cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
        - name: GOPATH
          value: /tmp/go
        - name: GOCACHE
          value: /tmp/.cache/go-build
        - name: GOMODCACHE
          value: /tmp/go/pkg/mod
        - name: CGO_ENABLED
          value: "0"
      resources:
        requests:
          cpu: "100m"
          memory: "256Mi"
        limits:
          cpu: "1500m"
          memory: "1536Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: go-cache
          mountPath: /tmp/.cache/go-build
        - name: go-mod-cache
          mountPath: /tmp/go/pkg/mod
        - name: tools-volume
          mountPath: /tools

    - name: node-builder
      image: ${NODE_BUILDER_IMAGE}
      command: ['cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
        - name: NPM_CONFIG_CACHE
          value: /tmp/.cache/npm
        - name: NEXT_TELEMETRY_DISABLED
          value: "1"
        - name: NODE_OPTIONS
          value: "--max-old-space-size=1536"
      resources:
        requests:
          cpu: "100m"
          # request == limit: next build spikes to ~2Gi. With request 256Mi the
          # container sits far over request during the build and is the kubelet's
          # first eviction victim under node MemoryPressure (pod evicted, dind
          # SIGTERM-exit-0, others 137 — builds #136/#139). Reserving the full 2Gi
          # protects it from node-pressure eviction.
          memory: "2Gi"
        limits:
          cpu: "1500m"
          memory: "2Gi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: npm-cache
          mountPath: /tmp/.cache/npm

    - name: docker
      image: ${DOCKER_CLI_IMAGE}
      command: ['sh', '-c', 'cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
        - name: DOCKER_HOST
          value: tcp://localhost:2375
        - name: DOCKER_TLS_CERTDIR
          value: ""
      resources:
        requests:
          cpu: "50m"
          memory: "64Mi"
        limits:
          cpu: "250m"
          memory: "128Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: tools-volume
          mountPath: /tools

    - name: dind
      image: ${DOCKER_DIND_IMAGE}
      securityContext:
        privileged: true
      env:
        - name: DOCKER_TLS_CERTDIR
          value: ""
      args:
        - --host=tcp://0.0.0.0:2375
        - --host=unix:///var/run/docker.sock
      resources:
        requests:
          cpu: "250m"
          # request == limit (1536Mi): dind burns memory building/exporting the
          # 4 images. With request 512Mi it runs over request and is evicted
          # under node MemoryPressure (#138/#141 die in the docker stage).
          # 1536Mi reserved protects it; 4Gi over-commits the node (caused the
          # next-build eviction in #136). Root crash was docker:29, now pinned 24.
          memory: "1536Mi"
          # ephemeral-storage: every build dies the moment dind extracts the 2nd
          # image's base layers (#143: golang:1.25-alpine extract -> channel drop).
          # The docker-graph-storage emptyDir counts as pod ephemeral storage; with
          # NO request, any usage is "over request" => this pod is the kubelet's #1
          # DiskPressure eviction victim (dind SIGTERM exit-0, others 137). Reserve
          # it so the build survives layer extraction.
          ephemeral-storage: "4Gi"
        limits:
          cpu: "1500m"
          memory: "1536Mi"
          ephemeral-storage: "8Gi"
      volumeMounts:
        - name: docker-graph-storage
          mountPath: /var/lib/docker
        - name: docker-certs
          mountPath: /certs
        - name: workspace-volume
          mountPath: /home/jenkins/agent
"""
) {
    // SURVIVE managed-control-plane / node-pressure flaps: the in-cluster JNLP4
    // channel drops mid-build (ClosedChannelException, pod "removed or offline")
    // when the Beget-managed apiserver flaps or the node evicts under memory
    // pressure (observed builds #136/#138). Retry ONLY on agent/channel loss —
    // a fresh pod is provisioned and the body reruns. Real failures
    // (compile/test/push) don't match agent() and still fail fast. Mirrors
    // jenkins-pipelines kubePodTemplate.
    retry(count: 5, conditions: [agent()]) {
    node(podLabel) {
        cleanWs()

        def commitAuthor  = ''
        def commitMessage = ''
        def resolvedTag   = ''
        def currentStageName = 'bootstrap'

        def runStage = { String name, Closure body ->
            currentStageName = name
            stage(name) { body() }
        }

        try {
            runStage('Checkout') {
                checkout scm
                commitAuthor  = sh(script: "git log -1 --pretty=format:'%an'", returnStdout: true).trim()
                commitMessage = sh(script: "git log -1 --pretty=format:'%s'", returnStdout: true).trim()
                def sha       = sh(script: 'git rev-parse --short=8 HEAD', returnStdout: true).trim()
                def tagOnHead = sh(script: 'git tag --points-at HEAD', returnStdout: true).trim()
                resolvedTag   = tagOnHead ?: sha
                env.RESOLVED_TAG = resolvedTag
                echo "Image tag: ${resolvedTag}  (commit: ${sha})"
            }

            // ── Go backend ────────────────────────────────────────────────
            container('go-builder') {
                runStage('Toolchain (Go)') {
                    sh '''
                        set -eux
                        go version
                        apk add --no-cache helm git >/dev/null 2>&1 || true
                        helm version --short
                    '''
                }

                runStage('Backend tests') {
                    dir('backend') {
                        sh 'go test ./... -count=1'
                    }
                }

                runStage('Backend build') {
                    dir('backend') {
                        sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/server ./cmd/server'
                    }
                }

                runStage('GitOps-agent tests') {
                    dir('gitops-agent') {
                        sh 'go test ./... -count=1'
                    }
                }

                runStage('GitOps-agent build') {
                    dir('gitops-agent') {
                        sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/gitops-agent ./cmd/gitops-agent'
                    }
                }

                runStage('Portainer-agent tests') {
                    dir('portainer-agent') {
                        sh 'go test ./... -count=1'
                    }
                }

                runStage('Portainer-agent build') {
                    dir('portainer-agent') {
                        sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/portainer-agent ./cmd/portainer-agent'
                    }
                }

                runStage('Helm lint + render') {
                    sh """
                        set -eux
                        helm lint helm/dada-cloud-console
                        helm template dada-cloud-console helm/dada-cloud-console \
                          --namespace devops-tools \
                          --set backend.image.tag=${resolvedTag} \
                          --set frontend.image.tag=${resolvedTag} \
                          --set gitopsAgent.image.tag=${resolvedTag} \
                          --set portainerAgent.image.tag=${resolvedTag} \
                          --set ingress.host=console.dada-tuda.ru \
                          > /tmp/dada-cloud-console-rendered.yaml
                        echo "Rendered \$(wc -l < /tmp/dada-cloud-console-rendered.yaml) lines"
                    """
                }
            }

            // ── Node.js frontend ──────────────────────────────────────────
            container('node-builder') {
                runStage('Frontend install') {
                    dir('frontend') {
                        // @dada/* deps resolve from internal Nexus npm (.npmrc).
                        // NEXUS_NPM_AUTH = base64("user:pass"); .npmrc expands it.
                        withCredentials([usernamePassword(
                                credentialsId: 'docker-nexus-admin-psws',
                                usernameVariable: 'NEXUS_USER',
                                passwordVariable: 'NEXUS_PASS'
                        )]) {
                            sh '''
                                set -eu
                                export NEXUS_NPM_AUTH=$(printf '%s:%s' "$NEXUS_USER" "$NEXUS_PASS" | base64 | tr -d '\\n')
                                npm ci
                            '''
                        }
                    }
                }

                runStage('Frontend typecheck + build') {
                    dir('frontend') {
                        withEnv([
                            "NEXT_PUBLIC_AUTH_MODE=${NEXT_PUBLIC_AUTH_MODE}",
                            "NEXT_PUBLIC_KEYCLOAK_ISSUER=${NEXT_PUBLIC_KEYCLOAK_ISSUER}",
                            "NEXT_PUBLIC_OIDC_CLIENT_ID=${NEXT_PUBLIC_OIDC_CLIENT_ID}",
                        ]) {
                            sh '''
                                set -eux
                                node -e "const p=require('./package.json'); process.exit(p.scripts && p.scripts.typecheck ? 0 : 1)" \
                                  && npm run typecheck || echo "No typecheck script — skip"
                                node -e "const p=require('./package.json'); process.exit(p.scripts && p.scripts.lint ? 0 : 1)" \
                                  && npm run lint || echo "No lint script — skip"
                                npm run build
                            '''
                        }
                    }
                }
            }

            // ── Docker ────────────────────────────────────────────────────
            container('docker') {
                runStage('Docker build') {
                    sh """
                        set -eux
                        docker version
                        docker build \\
                          -t ${BACKEND_IMAGE}:${resolvedTag} \\
                          -f backend/Dockerfile backend
                        docker build \\
                          -t ${GITOPS_AGENT_IMAGE}:${resolvedTag} \\
                          -f gitops-agent/Dockerfile gitops-agent
                        docker build \\
                          -t ${PORTAINER_AGENT_IMAGE}:${resolvedTag} \\
                          -f portainer-agent/Dockerfile portainer-agent
                    """
                    // Frontend image: npm ci inside the build pulls @dada/* from
                    // Nexus. Auth is passed as a BuildKit secret (never baked into
                    // a layer). NEXT_PUBLIC_* are build args (baked — they are public).
                    withCredentials([usernamePassword(
                            credentialsId: 'docker-nexus-admin-psws',
                            usernameVariable: 'NEXUS_USER',
                            passwordVariable: 'NEXUS_PASS'
                    )]) {
                        sh """
                            set -eu
                            printf '%s:%s' "\${NEXUS_USER}" "\${NEXUS_PASS}" | base64 | tr -d '\\n' > /tmp/nexus_npm_auth
                            DOCKER_BUILDKIT=1 docker build \\
                              --secret id=nexus_npm_auth,src=/tmp/nexus_npm_auth \\
                              --build-arg NEXT_PUBLIC_AUTH_MODE=${NEXT_PUBLIC_AUTH_MODE} \\
                              --build-arg NEXT_PUBLIC_KEYCLOAK_ISSUER=${NEXT_PUBLIC_KEYCLOAK_ISSUER} \\
                              --build-arg NEXT_PUBLIC_OIDC_CLIENT_ID=${NEXT_PUBLIC_OIDC_CLIENT_ID} \\
                              -t ${FRONTEND_IMAGE}:${resolvedTag} \\
                              -f frontend/Dockerfile frontend
                            rm -f /tmp/nexus_npm_auth
                        """
                    }
                }

                // Push only on integration branches, not PRs
                def isPullRequest = (env.CHANGE_ID != null && env.CHANGE_ID != '')
                def shouldPush = !isPullRequest && (
                        env.BRANCH_NAME == 'main' ||
                        env.BRANCH_NAME == 'master' ||
                        env.BRANCH_NAME == 'develop'
                )

                if (shouldPush) {
                    runStage('Docker push') {
                        withCredentials([usernamePassword(
                                credentialsId: 'gh-token',
                                usernameVariable: 'GITHUB_USERNAME',
                                passwordVariable: 'GITHUB_TOKEN'
                        )]) {
                            sh """
                                set -eux
                                echo "\${GITHUB_TOKEN}" | docker login ${GITHUB_REGISTRY} -u \${GITHUB_USERNAME} --password-stdin
                                docker push ${BACKEND_IMAGE}:${resolvedTag}
                                docker push ${FRONTEND_IMAGE}:${resolvedTag}
                                docker push ${GITOPS_AGENT_IMAGE}:${resolvedTag}
                                docker push ${PORTAINER_AGENT_IMAGE}:${resolvedTag}
                                docker tag ${BACKEND_IMAGE}:${resolvedTag} ${BACKEND_IMAGE}:latest
                                docker tag ${FRONTEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:latest
                                docker tag ${GITOPS_AGENT_IMAGE}:${resolvedTag} ${GITOPS_AGENT_IMAGE}:latest
                                docker tag ${PORTAINER_AGENT_IMAGE}:${resolvedTag} ${PORTAINER_AGENT_IMAGE}:latest
                                docker push ${BACKEND_IMAGE}:latest
                                docker push ${FRONTEND_IMAGE}:latest
                                docker push ${GITOPS_AGENT_IMAGE}:latest
                                docker push ${PORTAINER_AGENT_IMAGE}:latest
                                docker rmi ${BACKEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:${resolvedTag} ${GITOPS_AGENT_IMAGE}:${resolvedTag} ${PORTAINER_AGENT_IMAGE}:${resolvedTag} || true
                            """
                        }
                    }

                    // GitOps write-back: pin the built tag into the ArgoCD source
                    // so prod rolls. No image-updater exists — this commit IS the
                    // deploy trigger. Uses the same gh-token PAT as the registry push.
                    runStage('GitOps write-back') {
                        withCredentials([usernamePassword(
                                credentialsId: 'gh-token',
                                usernameVariable: 'GIT_USERNAME',
                                passwordVariable: 'GIT_TOKEN'
                        )]) {
                            sh """
                                set -eu
                                apk add --no-cache git yq >/dev/null 2>&1 || true
                                rm -rf /tmp/argo-infra
                                git clone --depth 1 --branch ${ARGO_BRANCH} \
                                  https://\${GIT_USERNAME}:\${GIT_TOKEN}@${ARGO_REPO} /tmp/argo-infra
                                cd /tmp/argo-infra
                                export TAG='${resolvedTag}'
                                yq -i '(.backend.image.tag, .frontend.image.tag, .gitopsAgent.image.tag, .portainerAgent.image.tag) = strenv(TAG) | (.backend.image.tag, .frontend.image.tag, .gitopsAgent.image.tag, .portainerAgent.image.tag) style="double"' ${ARGO_VALUES_PATH}
                                git config user.email 'platform-bot@dada-tuda.ru'
                                git config user.name  'DADA Platform Bot'
                                if git diff --quiet -- ${ARGO_VALUES_PATH}; then
                                    echo "Tag already ${resolvedTag} — nothing to write back"
                                else
                                    git add ${ARGO_VALUES_PATH}
                                    git commit -m "deploy(cloud-console): pin to ${resolvedTag} (build #${env.BUILD_NUMBER})"
                                    git push origin ${ARGO_BRANCH}
                                    echo "Wrote ${resolvedTag} -> ${ARGO_VALUES_PATH} on ${ARGO_BRANCH}"
                                fi
                            """
                        }
                    }
                } else {
                    echo "Docker push skipped (PR or non-deploy branch)"
                }
            }

        } catch (err) {
            currentBuild.result = 'FAILURE'
            throw err
        }

        if (currentBuild.result != 'FAILURE') {
            echo "✅ DADA Cloud Console — ${resolvedTag}"
            echo "   Backend:         ${BACKEND_IMAGE}:${resolvedTag}"
            echo "   Frontend:        ${FRONTEND_IMAGE}:${resolvedTag}"
            echo "   GitOps Agent:    ${GITOPS_AGENT_IMAGE}:${resolvedTag}"
            echo "   Portainer Agent: ${PORTAINER_AGENT_IMAGE}:${resolvedTag}"
        }
    }
    }
}
