# release-gate — 发版前的硬门禁

一个问题：**这棵树可以变成一次发布吗？**

```bash
scripts/release-gate.sh --tier all --version v0.6.5   # 发版：必须 PASS 且 releasable
scripts/release-gate.sh --tier static                 # 开发循环里的自检
scripts/release-gate.sh --tier live --profile smoke   # 只跑最快的真机冒烟
scripts/release-gate.sh --list                        # 列出所有检查项
```

报告写到 `tmp/release-gate-<stamp>/`：`summary.json`（机器读，`pre-push` 钩子用它）、
`summary.md`（人读）、`checks/*.log`（逐项日志）、`evidence/<suite>/`（探针证据）。

## 为什么是「门禁」而不是「清单」

至今每一个到达用户的回归，都通过了当时的全部检查：代码能编译、单测全绿、缺陷长在没人
串起来的那道缝里——`accessRules` 少一行的路由（线上 403）、存得下却永远跑不起来的画布、
重建了却从没被真正服务过的二进制。它们不是「测试」问题，是**发布**问题：能抓住它们的检查
早就存在，只是没有任何东西强制它们在打 tag 之前跑。

所以这里只做三件事：固定顺序跑（static → build → live）；写一个机器可读的结论；
**当一个必过项被跳过、被阻塞、或验证的和要发的是两个产物时，拒绝说「可以发」**。

## 三个层级

| 层级 | 内容 | 在哪里跑 |
| --- | --- | --- |
| `static` | 密钥/本地指纹扫描、改动文件的 gofmt、vet/build、RBAC 路由契约、迁移只追加、全量后端测试、前端 typecheck + 单测、CHANGELOG/发版说明 | 任何机器，CI 也跑 |
| `build` | 前端生产构建、打包演练 `scripts/release-drill.sh`、记录产物指纹、`internal/webfs/dist` 不得入库 | 工作站（需要 npm） |
| `live` | `readyz`、**线上 bundle == 本次构建的 bundle**、`suites.tsv` 里的真机接口/浏览器套件 | 工作站 + 一台真机部署 |

## 状态与退出码

`PASS` 跑了且成立 · `FAIL` 跑了但不成立 · `SKIP` 必过项没跑（判定 INCOMPLETE）·
`MISSING` 真机套件脚本不在这台机器上（判定 INCOMPLETE）。

退出码只有三个：`0` PASS、`1` FAIL、`2` INCOMPLETE。没有「警告放行」。

## 红线

1. **打 `v*` tag 前必须有一次 `--tier all --version <tag>` 的 PASS，且 `releasable=true`**。
   `scripts/git-hooks/pre-push` 会核对报告里的 `git.rev` 是否就是被 push 的那个 commit、
   `version` 是否就是 tag、报告是否在 72 小时内、`built == served`。核对不过就拒绝推送。
2. **不允许用 `--only`/`--skip` 剪裁出来的结果发版**：剪裁过的运行脚本会自己把 `releasable`
   判为 `false`（必过项被跳过则是 INCOMPLETE）。
3. **不允许「验证 A 部署、发布 B 产物」**：线上 `index-*.js` 必须等于本次构建产出的
   那个哈希，否则 `l02.served-fingerprint` 直接 FAIL。
4. **真机套件脚本缺失等于阻塞，不是「跳过」**：`MISSING` 会让判定停在 INCOMPLETE。
5. **发布跑必须从干净的树开始**：带 `--version` 时工作区有改动直接 FAIL（避免
   「验证的是未提交的内容、发的是另一个 commit」）。
6. 本文件、`scripts/release-gate.sh`、`suites.tsv`、`pre-push` 都在版本控制里；
   机房地址、账号、口令一律只从环境变量来，不入库（`scripts/secret-scan.sh` 守这一条）。
7. **钩子自己也被测**：`scripts/release-gate/selftest.sh` 用 9 个用例反过来测
   `pre-push`（无报告/FAIL/commit 不符/版本不符/过期/`releasable=false`/删 tag/放行/分支不拦），
   并作为必过项 `s12.gate-selftest` 跑在 `static` 层。一个永远 `exit 0` 的钩子和一个永远通过的
   钩子长得一模一样，所以这条不能省。

## 真机层级需要的环境变量

```bash
export RGX_BASE=http://<host>:<port>          # 被测部署
export RGX_USER=admin RGX_PASSWORD=...        # 该部署的管理员
export RGX_ENGINE=http://<engine>:8001        # 可选，部分套件会读
export RGX_GATE_DEPLOY_CMD='bash localenv/151-test/deploy.sh'   # 可选：先部署再验
export RGX_GATE_LIVE_DIR=localenv/probes      # 套件目录（默认）
export RGX_GATE_TMP_DIR=tmp                   # 临时探针目录（默认）
```

完整规范（用例矩阵、每轮抓到的缺陷、证据目录）见
`doc/environment/verification/46_发版前检查规范-门槛分级与脚本-2026-11-20.md`。
