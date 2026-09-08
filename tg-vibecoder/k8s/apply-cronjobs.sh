#!/usr/bin/env bash
#
# Applies the ingest CronJobs with the image the app Deployment is actually
# running.
#
# The build that produces this image is the platform's own app build, and it
# writes the resulting digest into the Deployment, not into these files. A
# CronJob manifest with a hand-written tag drifts away from the app the day
# after it is written, and the drift is silent: the cron keeps running an old
# image against a newer schema. Reading the digest back off the live
# Deployment at apply time keeps one source of truth.
#
# The channel cron ships suspended: it needs a channel slug in the
# vibecoder-config ConfigMap, which only the channel owner can supply.
set -euo pipefail

NS="${NS:-agent-sandbox-prod}"
DEPLOY="${DEPLOY:-tg-vibecoder-deploy}"
HERE="$(cd "$(dirname "$0")" && pwd)"

IMAGE="$(kubectl get deploy "$DEPLOY" -n "$NS" -o jsonpath='{.spec.template.spec.containers[0].image}')"
if [ -z "$IMAGE" ]; then
    echo "no image on deploy/$DEPLOY in $NS; is the app built?" >&2
    exit 1
fi
echo "pinning cronjobs to $IMAGE"

for f in news-ingest-cronjob.yaml channel-ingest-cronjob.yaml; do
    sed "s|IMAGE_PINNED_BY_APPLY_SCRIPT|$IMAGE|" "$HERE/$f" | kubectl apply -f -
done

kubectl get cronjob -n "$NS" | grep vibecoder
