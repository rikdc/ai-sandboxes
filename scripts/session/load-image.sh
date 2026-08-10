#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
source scripts/lib/msb-image.sh

tag=${1:?usage: load-image.sh TAG}

if msb_image_present "$tag"; then
  exit 0
fi

msb_load_image "$tag" "$tag"
