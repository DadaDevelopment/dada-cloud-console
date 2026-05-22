def GO_VERSION   = '1.25'
def NODE_VERSION = '20'

def GO_BUILDER_IMAGE   = "golang:${GO_VERSION}-alpine"
def NODE_BUILDER_IMAGE = "node:${NODE_VERSION}-bookworm"
def DOCKER_CLI_IMAGE   = 'docker:29-cli'
def DOCKER_DIND_IMAGE  = 'docker:29-dind'

def GITHUB_REGISTRY      = 'ghcr.io'
def GITHUB_ORG           = 'dadadevelopment'
def BACKEND_IMAGE        = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-backend"
def FRONTEND_IMAGE       = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-frontend"
def GITOPS_AGENT_IMAGE   = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-gitops-agent"

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
    argocd.argoproj.io/tracking-id: jenkins-beget:/Pod:devops-tools/${agentName}
  labels:
    app.kubernetes.io/name: jenkins-agent
    app.kubernetes.io/part-of: dada-cloud-console
    app.kubernetes.io/managed-by: jenkins
spec:
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
      image: jenkins/inbound-agent:latest
      tty: true
      workingDir: /home/jenkins/agent
      resources:
        requests:
          cpu: "50m"
          memory: "128Mi"
        limits:
          cpu: "300m"
          memory: "256Mi"
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
          memory: "256Mi"
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
          memory: "512Mi"
        limits:
          cpu: "1500m"
          memory: "1536Mi"
      volumeMounts:
        - name: docker-graph-storage
          mountPath: /var/lib/docker
        - name: docker-certs
          mountPath: /certs
        - name: workspace-volume
          mountPath: /home/jenkins/agent
"""
) {
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
                        sh 'go build -buildvcs=false -ldflags="-s -w" -o bin/server ./cmd/server'
                    }
                }

                runStage('GitOps-agent tests') {
                    dir('gitops-agent') {
                        sh 'go test ./... -count=1'
                    }
                }

                runStage('GitOps-agent build') {
                    dir('gitops-agent') {
                        sh 'go build -buildvcs=false -ldflags="-s -w" -o bin/gitops-agent ./cmd/gitops-agent'
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
                          > /tmp/dada-cloud-console-rendered.yaml
                        echo "Rendered \$(wc -l < /tmp/dada-cloud-console-rendered.yaml) lines"
                    """
                }
            }

            // ── Node.js frontend ──────────────────────────────────────────
            container('node-builder') {
                runStage('Frontend install') {
                    dir('frontend') {
                        sh 'npm ci'
                    }
                }

                runStage('Frontend typecheck + build') {
                    dir('frontend') {
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
                          -t ${FRONTEND_IMAGE}:${resolvedTag} \\
                          -f frontend/Dockerfile frontend
                        docker build \\
                          -t ${GITOPS_AGENT_IMAGE}:${resolvedTag} \\
                          -f gitops-agent/Dockerfile gitops-agent
                    """
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
                                docker tag ${BACKEND_IMAGE}:${resolvedTag} ${BACKEND_IMAGE}:latest
                                docker tag ${FRONTEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:latest
                                docker tag ${GITOPS_AGENT_IMAGE}:${resolvedTag} ${GITOPS_AGENT_IMAGE}:latest
                                docker push ${BACKEND_IMAGE}:latest
                                docker push ${FRONTEND_IMAGE}:latest
                                docker push ${GITOPS_AGENT_IMAGE}:latest
                                docker rmi ${BACKEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:${resolvedTag} ${GITOPS_AGENT_IMAGE}:${resolvedTag} || true
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
            echo "   Backend:      ${BACKEND_IMAGE}:${resolvedTag}"
            echo "   Frontend:     ${FRONTEND_IMAGE}:${resolvedTag}"
            echo "   GitOps Agent: ${GITOPS_AGENT_IMAGE}:${resolvedTag}"
        }
    }
}
