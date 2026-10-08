# RAGFlow-X

> An enterprise knowledge-infrastructure layer for RAGFlow in private data centers (Control Plane + Gateway). Do not fork RAGFlow; close the gap between enterprise production needs and a core RAG engine through its APIs.

[English](./README.md) | [简体中文](./README.zh-CN.md)

[![license](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](./LICENSE)
[![version](https://img.shields.io/badge/version-v0.2.0-green.svg)](./CHANGELOG.md)
[![Discord](https://img.shields.io/badge/Discord-community-blueviolet?logo=discord&logoColor=white)](https://discord.gg/fnc8Ds9nj)
![backend](https://img.shields.io/badge/backend-Go%20%2B%20Gin-informational.svg)
![frontend](https://img.shields.io/badge/frontend-React%20%2B%20shadcn--admin--kit-blueviolet.svg)

RAGFlow-X is built for private data centers and real enterprise production
environments. RAGFlow remains the core RAG execution and data plane. RAGFlow-X
closes the surrounding GAP with secure multi-tenant governance, RBAC/ABAC, an
OpenAI-compatible gateway, audit compliance, approvals, usage metering and
quota, observability, alerting, full-lifecycle knowledge operations, and a
unified conversation experience.

## Highlights

- **No fork, API-first.** RAGFlow operations use the official `/api/v1` HTTP API; operational data lives in an independent `rgx_*` PostgreSQL/SQLite database.
- **Security and governance.** Tenant governance, RBAC/ABAC, approvals, audit, observability, metering, retention, and a governed enterprise gateway.
- **Gateway.** `POST /v1/chat/completions` with authentication, RBAC, rate limiting, atomic quota reservation, metering, audit, 429 handling, and degradation.
- **Full-lifecycle knowledge system.** Dataset integration, parsing, quality, versioning, publication, consumption, feedback, evaluation, and operations.
- **Unified conversation center.** A consistent entry point for Chat, Search, and Agent experiences with role-aware routing, citations, rich content, tools, and export.
- **Production readiness.** Version/capability probing, contract tests, Docker Compose, structured logging, Prometheus, and audit hash-chain verification.

## Quick start

### Local development

```bash
make start-local
cd web && npm install && npm run dev
make status    # inspect services
make stop      # stop services
```

The local profile uses SQLite and a mock RAGFlow client.

### Connect a real RAGFlow instance

1. Copy `config/config.example.yaml` to `config/config.yaml`.
2. Configure `ragflow.base_url`, `api_key`, PostgreSQL, and Redis. Inject secrets through `RGX_*` environment variables.
3. Start with `make start`.

### Docker Compose

```bash
cd docker
cp .env.example .env
docker compose up -d --build
```

To use a prebuilt image:

```bash
docker pull ghcr.io/davidsche/ragflow-x:v0.2.0
```

Replace the `build:` section in `docker-compose.yml` with:

```yaml
image: ghcr.io/davidsche/ragflow-x:v0.2.0
```

## User documentation

| Document | Purpose |
|---|---|
| [User guide](./doc/user/en/user-guide.md) | Daily workflows for administrators and business users |
| [Built-in permission matrix](./doc/user/en/permission-matrix.md) | Built-in roles, resources, actions, and boundaries |
| [Operations runbook](./doc/user/en/operations-runbook-and-failure-drill-sop.md) | Initialization, upgrade, rollback, degradation, sync, and quota operations |
| [Audit-chain backup and repair SOP](./doc/user/en/audit-chain-backup-and-repair-sop.md) | Verification, backup, repair, and evidence archiving |

Chinese documentation is available in the
[user documentation index](./doc/user/README.md).

## Product video

![RAGFlow-X product overview](./doc/user/videos/ragflow-x-promo-en.mp4)

The 67-second overview is available for download at
[`doc/user/videos/ragflow-x-promo-en.mp4`](./doc/user/videos/ragflow-x-promo-en.mp4).

## Development checks

```bash
make build
make test
make vet
cd web && npm run typecheck && npm test
```

## Contributing

Before opening a pull request, read
[`CONTRIBUTING.md`](./CONTRIBUTING.md). Standard flow:

1. Fork and clone the repository:
   ```bash
   git clone git@github.com:DavidSche/ragflow-x.git
   ```
2. Create a feature branch: `git checkout -b feat/my-feature`.
3. Run local quality gates: `make check` and `cd web && npm test`.
4. Submit a pull request using the repository PR template.

## Community

- Discord: [join the community](https://discord.gg/fnc8Ds9nj)
- Issues: [GitHub Issues](https://github.com/DavidSche/ragflow-x/issues)
- Security: follow [`SECURITY.md`](./SECURITY.md); do not report vulnerabilities publicly.

## License

This project is licensed under GPL-3.0. Distributing binaries, modified source,
or derivative works requires distributing the corresponding source under the
same license.

RAGFlow remains an independent project and is integrated only through its
official HTTP API. Follow the upstream license terms when deploying a complete
solution.
