# 贡献指南

感谢关注 RAGFlow-X！本指南说明如何搭建环境、提交符合项目质量门禁的变更。

## 环境搭建

- 后端：Go 1.25+（以 `go.mod` 为准）、make、（可选）golangci-lint、（可选）Docker
- 前端：Node 20+、npm
- 集成测试（可选）：本地 PostgreSQL 16

```bash
go mod download
cd web && npm install
```

## 开发命令

| 命令 | 用途 |
| --- | --- |
| `make dev` / `make dev-web` | 启动后端 / 前端开发服务 |
| `make start-local` | 本地演示环境（SQLite + mock RAGFlow） |
| `make build` / `make build-web` | 构建后端 / 前端 |
| `make check` | 提交前全量检查：`go vet` + `go build` + `go test` + 前端 `tsc --noEmit` + 运维契约 |
| `make test-p0` / `make test-contract` | P0 场景回归 / 契约测试（CI 质量门禁） |
| `make test-integration` | PostgreSQL 集成测试（需设置 `RGX_TEST_POSTGRES_DSN`） |
| `make lint` | golangci-lint |
| `cd web && npm test` / `npm run test:e2e` | 前端单测 / Playwright E2E 冒烟 |

架构约定、权限控制规范与前后端接口规范见 `doc/`（AGENTS.md 有速览）。

## 提交规范

- 提交信息使用祈使句，聚焦“为什么”：如 `fix: 审批超时扫描跳过已完成步骤`。
- 一个提交聚焦一件事；大变更请拆分。
- 涉及行为变更时，同步更新：相关 `doc/` 设计文档、`CHANGELOG.md`（Unreleased 段）、
  前端 i18n（zh/en 两份都要）。

## Pull Request 要求

PR 描述请使用 [.github/pull_request_template.md](.github/pull_request_template.md)
提供的“测试证据”模板，核心要求：

1. **先识别契约，再写实现向测试**：明确本次变更涉及的 API / Provider / Repository
   契约与场景 ID；
2. **断言要有意义**：覆盖状态、错误语义、租户/项目边界、副作用与 `trace_id`，
   避免仅断言 HTTP 200 或 `error != nil`；
3. **避免脆弱测试**：不用固定 sleep、顺序耦合、共享可变状态、未分类的 `t.Skip`；
4. **SQLite / PostgreSQL 差异显式处理**；
5. 在 PR 模板的 Validation 清单里如实勾选已运行的门禁（`make test-p0` 等）。

## 质量门禁（CI 会强制执行）

- `go build ./...`、`go vet ./...`、`go test -shuffle=on ./...`
- `make test-p0`、`make test-contract`、changed-lines coverage（P0 ≥ 85%，P1 ≥ 75%）
- 前端 `npm run typecheck`、`npm test`、`npm run build`、E2E 冒烟
- golangci-lint

## 报告安全问题

**请不要通过公开 Issue 报告安全漏洞**，见 [SECURITY.md](SECURITY.md)。

## 许可

提交即表示你同意以 [GPL-3.0](LICENSE) 许可贡献代码。
