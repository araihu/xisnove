variable "VERSION" { default = "0.0.0" }
variable "COMMIT" { default = "0000000000000000000000000000000000000000" }
variable "BUILD_DATE" { default = "1970-01-01T00:00:00Z" }

group "default" {
  targets = ["server", "ui", "agent", "operator"]
}
group "test-amd64" {
  targets = ["server-amd64", "ui-amd64", "agent-amd64", "operator-amd64"]
}
group "test-arm64" {
  targets = ["server-arm64", "ui-arm64", "agent-arm64", "operator-arm64"]
}
group "oci-layout" {
  targets = ["server-oci", "ui-oci", "agent-oci", "operator-oci"]
}

target "common" {
  context = "."
  args = { VERSION = VERSION, COMMIT = COMMIT, BUILD_DATE = BUILD_DATE }
}

target "server" {
  inherits = ["common"]
  dockerfile = "build/package/Dockerfile.server"
  tags = ["ghcr.io/araihu/xisnove-server:${VERSION}"]
  platforms = ["linux/amd64", "linux/arm64"]
}
target "ui" {
  inherits = ["common"]
  dockerfile = "build/package/Dockerfile.ui"
  tags = ["ghcr.io/araihu/xisnove-ui:${VERSION}"]
  platforms = ["linux/amd64", "linux/arm64"]
}
target "agent" {
  inherits = ["common"]
  dockerfile = "build/package/Dockerfile.agent"
  tags = ["ghcr.io/araihu/xisnove-agent:${VERSION}"]
  platforms = ["linux/amd64", "linux/arm64"]
}
target "operator" {
  inherits = ["common"]
  dockerfile = "build/package/Dockerfile.operator"
  tags = ["ghcr.io/araihu/xisnove-operator:${VERSION}"]
  platforms = ["linux/amd64", "linux/arm64"]
}

target "server-amd64" {
  inherits = ["server"]
  platforms = ["linux/amd64"]
  tags = ["xisnove-server:test-amd64"]
  output = ["type=docker"]
}
target "ui-amd64" {
  inherits = ["ui"]
  platforms = ["linux/amd64"]
  tags = ["xisnove-ui:test-amd64"]
  output = ["type=docker"]
}
target "agent-amd64" {
  inherits = ["agent"]
  platforms = ["linux/amd64"]
  tags = ["xisnove-agent:test-amd64"]
  output = ["type=docker"]
}
target "operator-amd64" {
  inherits = ["operator"]
  platforms = ["linux/amd64"]
  tags = ["xisnove-operator:test-amd64"]
  output = ["type=docker"]
}

target "server-arm64" {
  inherits = ["server"]
  platforms = ["linux/arm64"]
  tags = ["xisnove-server:test-arm64"]
  output = ["type=docker"]
}
target "ui-arm64" {
  inherits = ["ui"]
  platforms = ["linux/arm64"]
  tags = ["xisnove-ui:test-arm64"]
  output = ["type=docker"]
}
target "agent-arm64" {
  inherits = ["agent"]
  platforms = ["linux/arm64"]
  tags = ["xisnove-agent:test-arm64"]
  output = ["type=docker"]
}
target "operator-arm64" {
  inherits = ["operator"]
  platforms = ["linux/arm64"]
  tags = ["xisnove-operator:test-arm64"]
  output = ["type=docker"]
}

target "server-oci" {
  inherits = ["server"]
  output = ["type=oci,dest=.artifacts/oci/xisnove-server.tar"]
}
target "ui-oci" {
  inherits = ["ui"]
  output = ["type=oci,dest=.artifacts/oci/xisnove-ui.tar"]
}
target "agent-oci" {
  inherits = ["agent"]
  output = ["type=oci,dest=.artifacts/oci/xisnove-agent.tar"]
}
target "operator-oci" {
  inherits = ["operator"]
  output = ["type=oci,dest=.artifacts/oci/xisnove-operator.tar"]
}
