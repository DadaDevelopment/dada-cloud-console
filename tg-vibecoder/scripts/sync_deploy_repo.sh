#!/usr/bin/env bash
#
# Publish the deploy tree to DadaDevelopment/tg-vibecoder.
#
# The platform builds an app from the ROOT of its linked repository: root_dir
# reaches framework detection but never reaches the Jenkins job, so a monorepo
# subdirectory cannot be a build context. tg-agent-tools solved this with a
# second, flat repository and then let it drift - the deploy repo sat on 757d7fa
# while the monorepo copy kept moving. This script exists so that drift is a
# command someone forgot to run, not a fact nobody can see: it rebuilds the tree
# from the monorepo every time and never edits the deploy repo in place.
#
# Layout published: the contents of tg-vibecoder/ at the root, plus agentkit/
# vendored beside them, which is what AGENTKIT_PATH=/app/agentkit expects.
#
# Usage: tg-vibecoder/scripts/sync_deploy_repo.sh [--push]
set -euo pipefail

REPO=DadaDevelopment/tg-vibecoder
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

rsync -a --delete \
    --exclude '__pycache__' --exclude '.pytest_cache' --exclude 'scripts/candidates.json' \
    "$HERE/tg-vibecoder/" "$STAGE/work/"
rsync -a --exclude '__pycache__' "$HERE/agentkit/" "$STAGE/work/agentkit/"

SHA="$(git -C "$HERE" rev-parse --short HEAD)"
echo "staged $(find "$STAGE/work" -type f | wc -l | tr -d ' ') files from $SHA"

if [ "${1:-}" != "--push" ]; then
    echo "dry run; pass --push to publish to $REPO"
    exit 0
fi

git -C "$STAGE/work" init -q -b main
git -C "$STAGE/work" add -A
git -C "$STAGE/work" -c user.email=agent@dada-tuda.ru -c user.name=dada-agent \
    commit -q -m "sync from dada-cloud-console $SHA"
git -C "$STAGE/work" push -q --force "https://github.com/$REPO.git" main
echo "pushed $SHA to $REPO"
