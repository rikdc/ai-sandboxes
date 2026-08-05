variable "NODE_IMAGE" {}
variable "CLAUDE_CODE_VERSION" {}
variable "CODEX_VERSION" {}
variable "TEA_IMAGE" {}
variable "GH_APT_KEY_FINGERPRINT" {}

group "default" { targets = ["claude", "codex"] }

target "common" {
  platforms = ["linux/arm64"]
}
target "base" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/base/Dockerfile"
  args = { NODE_IMAGE = NODE_IMAGE, TEA_IMAGE = TEA_IMAGE, GH_APT_KEY_FINGERPRINT = GH_APT_KEY_FINGERPRINT }
  tags = ["ai-sandboxes-base:local"]
}
target "claude" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/claude/Dockerfile"
  contexts = { base = "target:base" }
  args = { CLAUDE_CODE_VERSION = CLAUDE_CODE_VERSION }
  tags = ["ai-sandboxes-claude:local"]
}
target "codex" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/codex/Dockerfile"
  contexts = { base = "target:base" }
  args = { CODEX_VERSION = CODEX_VERSION }
  tags = ["ai-sandboxes-codex:local"]
}
