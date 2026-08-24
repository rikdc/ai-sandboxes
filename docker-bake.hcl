variable "NODE_IMAGE" {}
variable "CLAUDE_CODE_VERSION" {}
variable "CLAUDE_RELEASE_KEY_FINGERPRINT" {}
variable "CODEX_VERSION" {}
variable "OPENCODE_VERSION" {}
variable "TEA_IMAGE" {}
variable "GH_APT_KEY_FINGERPRINT" {}
variable "SHARED_STATE_ID" { default = "" }
variable "SHARED_STATE_QUOTA" { default = "" }
# USER_CONFIG_DIR is the resolved user configuration directory (see
# scripts/lib/config-dir.sh). It is exposed to the tools/claude/codex/opencode
# targets as a BuildKit named local context ("userconfig") so user files are
# consumed directly from outside the repository and never copied into the
# checkout or swept into the main build context. scripts/build sets it;
# invoking Bake directly requires setting it too.
# shellcheck disable=SC2034 # Consumed by docker buildx bake.
variable "USER_CONFIG_DIR" {}

group "default" { targets = ["claude", "codex", "opencode"] }

target "common" {
  platforms = ["linux/arm64"]
}
target "base" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/base/Dockerfile"
  args = {
    NODE_IMAGE = NODE_IMAGE
    TEA_IMAGE = TEA_IMAGE
    GH_APT_KEY_FINGERPRINT = GH_APT_KEY_FINGERPRINT
  }
  tags = ["ai-sandboxes-base:local"]
}
target "tools" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/tools/Dockerfile"
  contexts = { base = "target:base", userconfig = USER_CONFIG_DIR }
  args = { SHARED_STATE_ID = SHARED_STATE_ID, SHARED_STATE_QUOTA = SHARED_STATE_QUOTA }
  tags = ["ai-sandboxes-tools:local"]
}
target "claude" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/claude/Dockerfile"
  contexts = { base = "target:tools", userconfig = USER_CONFIG_DIR }
  args = {
    CLAUDE_CODE_VERSION = CLAUDE_CODE_VERSION
    CLAUDE_RELEASE_KEY_FINGERPRINT = CLAUDE_RELEASE_KEY_FINGERPRINT
  }
  tags = ["ai-sandboxes-claude:local"]
}
target "codex" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/codex/Dockerfile"
  contexts = { base = "target:tools", userconfig = USER_CONFIG_DIR }
  args = { CODEX_VERSION = CODEX_VERSION }
  tags = ["ai-sandboxes-codex:local"]
}
target "opencode" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/opencode/Dockerfile"
  contexts = { base = "target:tools", userconfig = USER_CONFIG_DIR }
  args = { OPENCODE_VERSION = OPENCODE_VERSION }
  tags = ["ai-sandboxes-opencode:local"]
}
