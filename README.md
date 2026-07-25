# Xisnove

Xisnove is an API-first, cloud-native monitoring system written in Go. The
current milestone supports HTTP/TLS, TCP, and resolver-pinned DNS monitors
stored in SQLite, leased by capability to outbound Agents, projected into
health, and promoted into Incidents after failures or durable staleness.

This is intentionally smaller than the full v1 roadmap. PostgreSQL/Turso
profiles, Kubernetes discovery and CRDs, notifications, UI, CLI, and release
packaging remain future milestones.

The public contract is [api/openapi.yaml](api/openapi.yaml). Root code contains
the control plane and public Go SDK; `agent/` is an independently buildable Go
module that knows the control plane only through its generated API client.

- [v1 architecture](docs/superpowers/specs/2026-07-24-xisnove-v1-design.md)
- [milestone implementation plan](docs/superpowers/plans/2026-07-24-milestone-1-first-observation.md)
- [protocol breadth plan](docs/superpowers/plans/2026-07-25-milestone-2a-protocol-breadth.md)
- [development guide](docs/development.md)
- [first observation runbook](docs/operations/first-observation.md)

Quick verification:

```bash
make check
go test -race ./integration -run 'TestFirstObservation|TestProtocolBreadth'
```
