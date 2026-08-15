# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

X-9 is initially for a technically proficient homelab operator who needs to see and act on the health of infrastructure spread across Kubernetes clusters, physical machines, VPS nodes, DNS providers, and private networks. Small-team collaboration, roles, and audit requirements remain an open product decision rather than a v1 assumption.

## Product Purpose

X-9 provides an external monitoring control plane that can observe infrastructure without sharing its failure domain. Success means an operator can discover monitorable resources, choose what should be monitored, understand current and recent health, and receive actionable notifications when state changes.

## Positioning

X-9 combines an API-first monitoring control plane with deployable agents that probe private networks and discover Kubernetes resources under read-only RBAC. Discovery produces a catalog for operator review; it does not silently create monitors.

## Operating Context

The control plane normally runs in a cloud environment, external VPS, or separate Kubernetes cluster. Agents run near private targets, including homelab Kubernetes clusters and hosts reachable through Tailscale. The current pilot includes Kubernetes services, HTTP routes and ingresses, Cloudflare-backed endpoints, DNS resolvers, physical nodes, and VPS nodes.

Operators use a server-rendered web BFF and a separate CLI. Both consume the public API or generated Go SDK and never access storage directly.

## Capabilities and Constraints

- API-first Go control plane with a public OpenAPI contract, oapi-codegen, and generated Go SDK.
- Hexagonal architecture with relational storage implementations backed by sqlc. Supported targets include SQLite, managed Turso/libSQL, and PostgreSQL.
- Kubernetes CRDs and an operator may declaratively materialize monitors and Agent credentials while the server remains relationally backed.
- Agents perform probes and optional discovery. Kubernetes discovery uses read-only RBAC and preserves operator choice before monitor creation.
- Notifications use a relational outbox and a provider abstraction compatible with the maintained Shoutrrr fork.
- UI and CLI are separate Go modules in the same repository. The UI is a BFF over the public API.
- Deployments must support raw release binaries, OCI images, Docker, and Kubernetes through Helm.
- Live status updates through BFF-owned SSE are planned; authoritative API reads remain the source of truth.
- The monitor detail workspace should use Goshtoso's drawer component while preserving URL, selected row, focus, and accessible selection semantics.
- The persistent application shell should expose Goshtoso's global search with `Ctrl+K`/`⌘K`. Navigation commands may remain local, but operational resources are queried and ranked by the Xisnove server through the public API and proxied by the BFF. The browser must not download or retain the complete operational index.

## Brand Commitments

Use `X-9` as the working shorthand while technical identifiers remain `xisnove`. The application shell now carries the organization-approved Xisnove V10 identity; product-name collision research remains a separate naming decision and does not authorize a repository-wide rename.

Do not create repository-local alternative marks. The shell consumes pinned copies of `logos/xisnove-logo.svg`, `logos/xisnove-mark*.svg`, and `logos/xisnove-favicon.svg` from `github.com/araihu/assets` commit `ab01f1a0f592e4f1398173df04e4f8fc013cb21a`; versioned same-origin routes preserve deterministic availability and rolling-deploy compatibility.

The voice is concise, operational, and evidence-led. UI copy should describe infrastructure state and available action, not implementation details.

## Evidence on Hand

- A live homelab pilot currently reports three real monitors and a Kubernetes discovery catalog.
- The repository contains the public API, generated SDK, server, Agent, relational storage, UI BFF, browser tests, deployment resources, and design state/action ledger.
- No customer claims, commercial benchmarks, or testimonials exist and must not be fabricated. Canonical Xisnove V10 assets come only from the pinned `github.com/araihu/assets` source; Arai Hû alone uses the cloud symbol and Xisnove retains its independent monitoring-signal mark.

## Product Principles

1. Observe from outside the failure domain whenever possible.
2. Keep operator intent between discovery and monitoring.
3. Make current state, evidence freshness, and the next safe action immediately legible.
4. Preserve provider-neutral contracts and self-hosted deployment paths.
5. Treat recovery, idempotency, and partial connectivity as normal operating conditions.

## Accessibility & Inclusion

The web UI must support keyboard operation, visible focus, semantic native controls, responsive use at 390 px and 1440 px, and both light and dark modes across Goshtoso and Minimal themes.
