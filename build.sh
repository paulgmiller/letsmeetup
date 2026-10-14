#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "$0")"
commit=$(git rev-parse --short HEAD)
docker build . -t "paulgmiller/letsmeetup:$commit"
docker push "paulgmiller/letsmeetup:$commit"
