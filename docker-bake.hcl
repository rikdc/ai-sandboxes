variable "NODE_IMAGE" { default = "node:22-bookworm" }
variable "CLAUDE_CODE_VERSION" { default = "2.1.221" }
variable "CODEX_VERSION" { default = "0.145.0" }
variable "TEA_IMAGE" { default = "gitea/tea@sha256:3492546d2267fd74386c108fa73672a5d78ca995b37b6cfd84f2d429fafc6612" }

group "default" { targets = ["claude", "codex"] }

target "common" {
  platforms = ["linux/arm64"]
  args = { NODE_IMAGE = NODE_IMAGE, TEA_IMAGE = TEA_IMAGE }
}
target "base" {
  inherits = ["common"]
  context = "."
  dockerfile = "images/base/Dockerfile"
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
