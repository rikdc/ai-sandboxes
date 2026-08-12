#!/usr/bin/env bash
set -o pipefail
cd "$(dirname "$0")/../.." || exit 1
source scripts/lib/msb-image.sh || exit 1

tag=${1:?usage: load-image.sh TAG}

if msb_image_present "$tag"; then
  exit 0
fi

msb_load_image "$tag" "$tag"
