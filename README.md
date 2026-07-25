# Xisnove

Xisnove is an API-first, cloud-native monitoring system written in Go. The
current milestone delivers one complete path: an HTTP monitor stored in
SQLite, leased to an outbound Agent, projected into health, and promoted to a
critical Incident after its failure threshold.

This is intentionally smaller than the full v1 roadmap. PostgreSQL/Turso
profiles, Kubernetes discovery and CRDs, notifications, UI, CLI, and release
packaging remain future milestones.

The public contract is [api/openapi.yaml](api/openapi.yaml). Root code contains
the control plane and public Go SDK; `agent/` is an independently buildable Go
module that knows the control plane only through its generated API client.

- [v1 architecture](docs/superpowers/specs/2026-07-24-xisnove-v1-design.md)
- [milestone implementation plan](docs/superpowers/plans/2026-07-24-milestone-1-first-observation.md)
- [development guide](docs/development.md)
- [first observation runbook](docs/operations/first-observation.md)

Quick verification:

```bash
make check
```
