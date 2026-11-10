# 审计链备份与修复 SOP

> 适用版本：RAGFlow-X v0.5.0（迁移 v162 起）｜ 更新日期：2026-11-10

## 适用范围

本文适用于 PostgreSQL 部署的 RAGFlow-X 生产环境。对象表为 `rgx_audit_log`，接口为：

- 审计链校验：`GET /api/v1/audit/verify`
- 审计链修复：`POST /api/v1/audit/repair`

修复接口要求平台管理员身份；普通租户管理员即使具备 `audit:read` 权限也不能调用。修复动作是对指定租户现存审计记录执行 re-anchor，不恢复已删除记录。

## 原则

1. **校验先行**：发现或怀疑链路异常时，先校验并保存结果，不要直接修复。
2. **备份先行**：任何修复前必须完成独立物理备份；备份文件是证据，不能被后续修复覆盖。
3. **最小操作**：只对报告异常或经负责人确认的租户执行修复。
4. **不直接改哈希**：禁止通过 SQL 手工修改 `prev_hash`、`hash`、`seq` 或 `at` 来“修复”链路。
5. **事后复验**：修复接口返回后，必须再调用校验接口确认 `valid=true`。

## 日常备份

### 1. 准备环境变量

脚本不内置连接串和口令。在运维终端临时导出：

- `DATABASE_URL`：PostgreSQL 连接 URL，建议使用最小权限运维账号。
- `RGX_AUDIT_BACKUP_DIR`：备份输出目录，应位于独立磁盘或备份挂载点。

### 2. 执行每日备份

Windows 使用：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/audit_backup.ps1
```

Linux/macOS 使用：

```bash
./scripts/audit_backup.sh
```

脚本会写入自定义格式的 PostgreSQL dump，生成同名 SHA-256 清单文件，并输出备份完成路径。manifest 记录文件大小、SHA-256 与时间戳，不记录连接凭据。

### 3. 保留策略

| 环境 | 最短保留期 | 备注 |
|---|---|---|
| 生产 | 180 天 | 每日备份，按合规要求可延长 |
| 预发布 | 30 天 | 用于演练修复流程 |

备份目录权限应限制为备份服务账号和审计管理员。超过保留期前，先确认备份已在异地或对象存储保存。

## 异常处置流程

### 第一步：记录现场

使用具备 `audit:read` 权限的账号调用：

```powershell
Invoke-RestMethod -Method Get -Uri "http://localhost:9191/api/v1/audit/verify" -Headers @{ Authorization = "Bearer <access-token>" }
```

如返回 `valid=false`，不要删除或重写任何审计数据，继续第二步。

### 第二步：备份当前表

使用上文备份脚本立即执行一次带时间戳的备份。若备份失败，不得进入修复步骤，应转为数据库管理员介入。

### 第三步：确认修复对象

从最近业务变更、审批操作和错误时间线确认异常租户。修复接口按 `tenant_id` 执行，不得批量猜测。

### 第四步：平台管理员修复

```powershell
$body = @{ tenant_id = "<tenant-id>" } | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri "http://localhost:9191/api/v1/audit/repair" -ContentType "application/json" -Headers @{ Authorization = "Bearer <platform-admin-token>" } -Body $body
```

接口会 re-anchor 指定租户现存记录，写入 `audit.repair` 审计，并再次校验。只有响应中 `valid=true` 才能结束修复。

### 第五步：归档证据

归档以下三类文件并记录修复单号：

1. 修复前备份 dump 与 manifest。
2. 修复前 `valid=false` 校验响应。
3. 修复后 `valid=true` 校验响应。

如需回查原始链路，使用备份恢复到隔离 PostgreSQL 实例，不要覆盖生产库。

## 验收标准

- 备份脚本在空 `DATABASE_URL` 或空备份目录时快速失败。
- dump 使用 PostgreSQL custom format，便于单表选择恢复。
- manifest 包含 SHA-256，且校验通过。
- 修复前有备份；修复后校验为 `valid=true`。
- 所有修复事件已生成 `audit.repair` 审计记录。
