#!/usr/bin/env bash
# one-shot post-ship probe (kept for the next cycle; sess-0918a already verified delivery)
set -u
export PATH="/opt/data/bin:$PATH"
cd /opt/data/projects/dada-cloud-console || exit 1
LAST=$(git rev-parse --short=8 HEAD)
IMG=$(kubectl get deploy -n argocd-prod dada-cloud-console-frontend -o jsonpath='{.spec.template.spec.containers[0].image}')
echo "HEAD: $LAST / prod image: $IMG"
if echo "$IMG" | grep -q "$LAST"; then echo "DELIVERED"; else echo "NOT-DELIVERED"; fi
