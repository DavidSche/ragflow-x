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
`MISSING` 真机套件脚本不在这台机器上（判定 INCOMPLETE）·
`N/A` 这项要对照的文档不在这棵树里（**只在 `--ci` 下存在**，见下）。

退出码只有三个：`0` PASS、`1` FAIL、`2` INCOMPLETE。没有「警告放行」。

### `N/A`：一种只在 CI 里有意义的「答不了」

`doc/` 被 `.gitignore:91` 排除（只放行 `doc/user/`），所以 `git clone` 出来的树里
没有 301/304/305、没有 `doc/specification/12`、没有标注手册。有五个 static 检查是
「拿代码和这些文档对照」，在没有文档的树上它们**答不了**，而不是**答了「一致」**。

- 检查脚本遇到这种情况退出 **3**，并在日志里写明是哪个文件、为什么没有；
- `scripts/release-gate.sh` 决定 3 的含义：`--ci` 运行记为 `N/A`（行仍在报告里，
  `required X/Y passed` 的分子不算它，判定写作 `PASS (ci: N not applicable)`），
  **其它任何运行都记为 `FAIL`**；
- `N/A` 永远换不到 `releasable=true`（`--ci` 本来就跑不了 live 层），而
  `pre-push` 要求每个必过项状态都是 `PASS`，所以**带 `N/A` 的报告推不了 tag**
  （`scripts/release-gate/selftest.sh` 第 9 例就是在测这句话）。

一句话：CI 可以「这一项在我的树里没有输入」，但只有工作站上那次带文档的
`--tier all` 才能说「都过了、可以发」。

### 快照：让 CI 至少能判「台账」（304 §7 第 26 条，2026-11-23 定）

`N/A` 诚实地承认了缺口，但它把「CI 绿」变成了「台账没查」。第 26 条选的路是
**受控快照**：`scripts/release-gate/docs-ledger.json` 入仓，`s19.docs-ledger-snapshot`
读它——schema、状态闭集、编号连续性、内部自洽（26 行 / 11 条偏差 / E 号尾部的
`next == last+1` / changelog 严格递减 / 305 小节号递增）都在**没有 `doc/` 的树上**判。

新鲜度用**内容指纹**判，不用时间戳：`generated_at` 只是来源记录，没有任何判断读它。
在有 `doc/` 的树上，三份源文档的 sha256 必须与快照逐字节一致，否则 **FAIL** 并打印
重新生成的那条命令；在干净树里则明说「新鲜度在本树无法证明」。

快照只在 `docs-ledger.sh` 的九项形状检查全过时才写得出来——一份坏文档不会被冻成
「已核」。它可以证明的边界写在文件里、也每跑一次都会打印一遍：**行的存在、词在闭集里、
编号没有洞、文档没变过；不能证明证据格的内容还支撑着旁边那句话。**

剩下的五个（`s13`/`s14`/`s15`/`s17`/`s21`）读的是文档里的**原句**（301 §11.1 的门禁行、
§9.4 的五句、§5.7 的阈值、304 §7 第 12 条的探针约定、标注手册 §4 第 3 条"读标注包而不是读工作表"），快照机制已经在了，但这四处
要把「原句」也一起冻进去才谈得上 CI 可判——见 304 §7 第 26 条的「还剩什么」。

### 选集本身就是被检查的面（`--only`/`--skip`，304 §4 E98/E102）

一条 check 没跑，和一条 check 跑了但没断言，在报告里长得一样——所以选集不再只是过滤器：

* id 集合**从 `release-gate.sh` 里 `run_check`/`run_suite` 的声明处读**（`known_ids()` 扫脚本自身），不第二次手写，新加 check 自动已知；
* `--only` 里任何一个 token 不在集合内 → **exit 2**，并把全部 id 打印出来。前缀不是简写：`--only=s16` 曾经一个都没选中，于是每条 check 落 `SKIP`、`required` 成 `0/0`、顶行打 `PASS`；
* `--skip` 的未知 token 同样 exit 2 —— 那个方向更安静：「以为排除了，其实还在跑」；
* `--only X --skip X` 这类**空选集 → exit 2**；
* 最后一道闸：`RUNS=0` 一律 exit 2，**绝不落 `PASS`**。

`selftest.sh` 有 6 例选集用例，并且 `--selftest` 会把四条规则各删一条造 4 个变异体，要求用例**全部变红**（`SELFTEST OK: 4 gate mutants refused`）——一条只为好看而存在的检查，本身就是这个门禁要防的东西。

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
