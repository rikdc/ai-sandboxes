#!/usr/bin/env bash

msb_image_present() {
  local tag=$1
  msb image list --quiet | grep -Fxq "$tag"
}

msb_load_image() {
  local docker_ref=$1 msb_ref=$2
  docker save "$docker_ref" | msb load --tag "$msb_ref"
}
