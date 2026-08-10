variable "NODE_IMAGE" {}
variable "CLAUDE_CODE_VERSION" {}
variable "CLAUDE_RELEASE_KEY_FINGERPRINT" {}
variable "CODEX_VERSION" {}
variable "TEA_IMAGE" {}
variable "GH_APT_KEY_FINGERPRINT" {}
variable "SHARED_STATE_ID" { default = "" }
variable "SHARED_STATE_QUOTA" { default = "" }
variable "MARKETPLACES_CONFIG" { default = "config/marketplaces.json" }

group "default" { targets = ["claude", "codex"] }

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
  contexts = { base = "target:base" }
  args = { SHARED_STATE_ID = SHARED_STATE_ID, SHARED_STATE_QUOTA = SHARED_STATE_QUOTA }
  tags = ["ai-sandboxes-tools:local"]
}
target "claude" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/claude/Dockerfile"
  contexts = { base = "target:tools" }
  args = {
    CLAUDE_CODE_VERSION = CLAUDE_CODE_VERSION
    CLAUDE_RELEASE_KEY_FINGERPRINT = CLAUDE_RELEASE_KEY_FINGERPRINT
    MARKETPLACES_CONFIG = MARKETPLACES_CONFIG
  }
  tags = ["ai-sandboxes-claude:local"]
}
target "codex" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/codex/Dockerfile"
  contexts = { base = "target:tools" }
  args = { CODEX_VERSION = CODEX_VERSION }
  tags = ["ai-sandboxes-codex:local"]
}
