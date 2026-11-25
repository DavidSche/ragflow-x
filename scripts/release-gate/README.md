# release-gate — 发版前的硬门禁

一个问题：**这棵树可以变成一次发布吗？**

```bash
scripts/release-gate.sh --tier all --version v0.6.5   # 发版：必须 PASS 且 releasable
scripts/release-gate.sh --tier static                 # 开发循环里的自检
scripts/release-gate.sh --tier live --profile smoke   # 只跑最快的真机冒烟
scripts/release-gate.sh --list                        # 列出所有检查项（人读：层、描述、doc 标记）
scripts/release-gate.sh --list=tsv                    # 同一声明，机器读：tier<TAB>id<TAB>命令<TAB>doc=0|1
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
| `static` | 密钥/本地指纹扫描、改动文件的 gofmt、vet/build、GORM 共享 builder 检查、RBAC 路由契约、迁移只追加、全量后端测试、前端 typecheck + 单测、OpenAPI 生成物新鲜度（`s25`）+ 兼容性（`s26`）、CHANGELOG/发版说明 | 任何机器，CI 也跑 |
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

第 ⑨ 项就是这一轮加的，理由是它自己撞上的两件事：304 §6 与 305 §5 都出现过「一行不再是行」——
上一轮的修补把一条版本记录插进了另一条的**行内**，于是 `v0.37 | … | 2026-11-24 |` 单独成行、
没有 `| 日期 |` 的头，终端里读起来完全正常。304 那行（`| v1.19 | …`）一直没被看见，因为 ③
只数「**能解析的**行」，看不见的那行只是从分母里消失了。现在两处都按形状判：头两格必须是日期与
`vX.Y`、结尾必须有 `|`，解析不了的行点名而不是跳过。305 §5 的**顺序与重号**当时故意不判——那是重编历史，是决定不是形状（304 §7 第 41 条）。
第 41 条在这一轮有了决定（E136）：**保留历史编号、按写入顺序排列**。于是 ⑨ 多判一条只关未来的规矩——
305 §5 头部那行 `治理起点 = YYYY-MM-DD` **之后**的记录必须号码唯一、严格递减，起点之前的 4 处逆序与
8 个重号按历史保留。起点日期是**从文档里读的**，不是脚本里的常量：写死一份，就等于把 305 §5 的一个
事实抄了第二遍，改动边界那天常量会安静地继续检查错误的那一群记录。缺了那行也是 FAIL——没有起点，
这条规矩就没有可判的范围，「看起来没问题」不是答案。

新鲜度用**内容指纹**判，不用时间戳：`generated_at` 只是来源记录，没有任何判断读它。
在有 `doc/` 的树上，三份源文档的 sha256 必须与快照逐字节一致，否则 **FAIL** 并打印
重新生成的那条命令；在干净树里则明说「新鲜度在本树无法证明」。

快照只在 `docs-ledger.sh` 的十一项形状检查全过时才写得出来——一份坏文档不会被冻成
「已核」。它可以证明的边界写在文件里、也每跑一次都会打印一遍：**行的存在、词在闭集里、
编号没有洞、文档没变过；不能证明证据格的内容还支撑着旁边那句话。**

剩下的六条（`s13`/`s14`/`s15`/`s17`/`s21`/`s23`）读的是文档里的**原句**（301 §11.1 的门禁行、
§9.4 的五句、§5.7 的阈值、304 §7 第 12 条的探针约定、标注手册 §4 第 3 条"读标注包而不是读工作表"、
手册 §2/§5.1 的 `(item_id, candidate_index)`），快照机制已经在了，但这六处
要把「原句」也一起冻进去才谈得上 CI 可判——见 304 §7 第 26 条的「还剩什么」。

它们八条（加上 `s18`/`s19`）在声明里都带 `doc` 标记，`--list` 会显示成 `(static, doc)`，
`--list=tsv` 是第四列。这个标记不是文档，是**选集的一处来源**：`s24.doc-present` 从它派生
「哪些行读文档」，并且反过来核对「读了文档文件的脚本必须有行声明 `doc`」——漏标会红，不会少跑一行。

### 一条规则只在 `go test` 里活着，就等于只在书里活着（`s21`/`s23`，304 §4 E112/E119）

这是两条同一句话的行。`s21` 盯的是「标注人读的是包，不是工作表」：包里不能有 `prior_*`/`gold_*`，
`labels --stub --packet` 写骨架前要自己验一遍。`s23` 盯的是「一条候选行算一个单位」：
方案 §9.1.3 一 说 E2 的分母是 `(item_id, candidate_index)`，而工具曾只认 `item_id`
（151 上 36 行读成 30 条断言，两条候选判断并成一次，哪个留下看文件顺序，**每个门禁都还是绿的**）。

两行都不重写 Go 里的断言，而是做 `go test` 做不到的三件事：

* **具名**：`-run` 正则**从具名清单派生**（`run_pattern="$(IFS='|'; printf '%s' "${REQUIRED_TESTS[*]}")"`），
  测试改名比删文件安静得多——套件照样 `ok`，只有清单会点名；
* **原句**：手册里那几句是人真正照着做的东西（`label_without_candidate_index`、31 个字符的键名、
  候选侧那张表）。代码改了而句子没改，就是规矩只活在一半人那里；
* **拒绝句留在源码里**：`assemble` 不接受候选侧标签文件，报错点名 `(item_id, candidate_index)`——
  这条如果消失，失败模式不是崩，是**一份更窄的金标**，每个检查都过得去。

`--selftest` 各自证明自己会红：`s21` 六条变异（手册丢旗子 / 被拿掉的那句回来 / 测试改名 /
剥离器不再剥离 / `goldtool packet` 从 usage 消失 / 手册里的 `--packet` 指向一个**磁盘上没有**的文件），
`s23` 三条（测试改名 / 手册删掉 key / `assemble` 删掉拒绝）。最后那条变异来自 E122：v1.13 把两份老
工作表的包派生成了 `packet-*.jsonl`，而 §5.1/§6 的 `--packet` 还写着 v1.12 的
`worksheet-*.packet.jsonl`——**文本自己跟自己一致**（0a 写出那个名字、第 1 步读它），grep 无论怎么
扫都看不出问题，证人只能是文件系统。所以这条检查读的是**输入是否真的在**（`--blind-out` 是输出，
不查）：`tmp/gold/` 在场的树上，§5 起每个 `--packet tmp/gold/…` 必须 `[ -f ]`。`s23` 还有一条**只在树里有真实工作表时**才跑得起来的断言：
拿 `tmp/gold/worksheet-cand-all-v1.jsonl` 重算一遍，**36 行 / 30 条断言**——数字从文件来，不从文档来。

### 「3 不是通过」：§9.4 的五条自检（`s14` / `cmd/assurance-quality`，304 §4 E80·E128）

301 §9.4 要求「复核质量自检跑在 CI 里，不靠人」。这里有两件不同的东西，别混：

| | 谁 | 读什么 | 判什么 |
| --- | --- | --- | --- |
| 离线一半 | `s14.assurance-quality`（static，required，`doc` 标记） | 跟踪的 Go 源 + 301 §9.4 原句 + 提交的 fixture | 五条（check/期望/失败含义）三元组**双向逐字**一致；6 条违规探针必须被点名；6 条不可核验探针必须落 `unverified` |
| 实库一半 | `cmd/assurance-quality --dsn/--since --receipt` | 151 上的 `rgx_test` | 五条各自的 `pass`/`unverified`/`fail`，收据 `O_EXCL` |

离线这一半为什么值得存在：它证明的是**仪器本身**会红，不是「今天数据干净」。`--selftest`
四条变异各自必须被拒：函数侧改写一句（措辞漂移）、函数侧删掉一行、文档侧删掉一行、
**把修订前那句抄回函数侧**。最后一条是本轮（E128）加的，也是最值钱的一条——301 §9.4 第 4 条
原先写着「`conflict_ref`（候选）出现在 `rgx_report_citation` 的次数 = 0」，而那张表 25 列里
没有 `conflict_ref`。门禁比的是整行，两侧抄了同一句不可实现的话，于是**绿恰恰因为两边错得一样**；
把它抄回去必须红，这条才是证据。

实库那一半的三条读法（runbook「复核质量自检」一节同文）：

1. **退出码 3 不是通过**：`unverified` 表示这一行窗口内没有分母（没有 replay、没有 `supported`
   行）。「没测」不能写成「没问题」，所以 3 与 1 一样阻塞；0 才是五条都有读数且都在期望内。
2. **Q-4 判形状不判重合**：候选是从**同一节**的检索结果里冻出来的，所以「候选 chunk 同时被引」
   是正确系统的形状（151 上 77 条），不是污染；这条查的是「有没有列能命名候选」与「每条引用可解析」。
3. **Q-5 的分母是弃权**，不是全部断言（A1-09 那条 15% 用的是全断言 CP 上界，两者不能换着说）。

收据（`--receipt`）用 `O_EXCL` 写：重跑要换文件名。一个能反复覆盖的收据，读起来像「每次都跑过」。

### 「干净克隆里也绿」有入口（`s22` / `make clean-clone`，304 §7 第 38 条、§4 E116）

这句话原先是三段手工动作：`git worktree`、把 gitignore 的 `doc/` 拷进去、还要记得目录得叫
`ragflow-x`。一轮里三次踩坑，两次是方法的坑（`--skip` 只生效一个；`cp -r doc ./doc` 多套一层，
`doc/` 于是"不存在"、五条 doc 行答 N/A 而读数还写成绿）。现在是一个入口：

```bash
make clean-clone            # 把 doc/ 拷进 worktree，跑 static 减去两条前端行（没有 node_modules）
make clean-clone-selftest   # 证明它读的是 worktree，不是你手上那棵脏树
bash scripts/release-gate/clean-clone.sh --ci   # 不拷 doc/：release.yml preflight 的形态
```

它 `--selftest` 的三条变异：① 干净检出必须 PASS；② 把 worktree 里的 `blind.go` 覆盖成一行
（若读的是你自己的树，这个污染就看不见）→ 必红且**点名** `s21.blind-packet`；③ 删掉拷进去的
`doc/`（若 doc 行其实答了 N/A，这条就不会红）→ 必红且点名 `s21`。

门禁里只有一条 3 秒的 `s22.clean-clone`（`--ci`，窄选集）：它证明**工具还能跑**，
不证明"这棵树干净"——后者归 `release.yml` preflight（每次打 tag 的真检出）与 `--selftest`。

### `doc/` 在场的那一半也有入口了（`s24` / `make doc-present`，304 §7 第 39 条、§4 E121）

`s22` 跑的是 `--ci`，那是**发布出去的检出**：`doc/` 不存在（`.gitignore:91` 是策略），八条
读文档的行全答 N/A。于是「在有文档的树上，那八行真的跑过」原先只有一个人工读者——
`clean-clone.sh --selftest` 的第 ③ 条，得有人去敲。

第 39 条当时把它记成「未做，需先定代价」，代价是**估**的（~10s）。量出来是 **2m32s**，差一个数量级；
更要紧的是 `clean-clone.sh` 只判**提交**，而新加的行还在工作区，于是那个检出里 `--only s23` 
直接答 `--only names no check in this file`——「在干净检出里跑 doc 行」在开发期永远是红的。
一条必然红、而且红得有理的行，读者会学会忽略它。两处改动把它变成能绿的：

```bash
make doc-present            # 造一份「此刻提交会带的内容 + doc/」的检出，跑那八行
make doc-present-selftest   # 证明它能为红：删掉检出里的 doc/、摘掉某行（含中文路径那行）的 doc 标记
```

* `clean-clone.sh --from-worktree`：`git ls-files --cached --others --exclude-standard`（**提交会带的东西**）
  覆盖到 HEAD 的 worktree 上。未提交的新文件在，被 ignore 的（`tmp/`、`node_modules/`、`doc/`）不在。
  这让「读文档的行不依赖未跟踪文件」**更准**而不是更松：`s23` 的真实工作表那一段在那里确实没跑
  （`tmp/gold/` 被 ignore），行照样 `[PASS]`——这正是要证明的。
* 逐行 `[PASS]`，不是「本次运行是绿的」：N/A 的行不打印 `[PASS]`，八行里三行答 N/A 的绿会被读成成功。
* 因为 2.5 分钟属于**发版前**而不是**提交前**，它是 `build` 层的行（`--tier all` 会跑），不是 static。

摘标记的变异有两条，因为「读了文档却没声明 `doc`」这条反向守卫自己也会瞎（E123）：它的判断式原来是
`doc/[A-Za-z0-9._/-]+\.md`，而 `assurance.sh` 读的是 `doc/specification/12_前后端接口交互规范.md`——
文件名带中文，ASCII 字符类在 `12_` 处断掉，`.md` 落不进匹配。于是**读规范的那一行**对这个守卫是不可见的：
摘掉 `s13` 的标记，选集还剩 7 条，`≥6` 的地板照样过，八条 doc 行静默变成七条。字符类改成「非空白」+ `.md` 锚
（路径里没有空格，`.md` 才挡得住散文），并加了变异 ③：摘 `s13` 的标记 → 必红且点名 `assurance.sh`。
**③ 在旧判断式下是绿的**，所以它是证据而不是装饰。① 现在还会区分两种红：输出里没有 `RELEASE GATE:` 这一行
说明克隆或门禁根本没跑到（例如上一轮残留的 worktree），那不是「doc 行没红」。

`s13`/`s14`/`s15`/`s17`/`s21`/`s23` 断言的**原句**本来就在仓里（`s14` 从跟踪的 Go 源、`s15` 从跟踪的
`prereg-e1-v1.json`、其余是脚本字面量、`s19` 从跟踪的快照）——所以第 26 条「把原句冻进快照」被
「测量代替实施」收口：冻结副本在无 `doc/` 的树上不多出一个证人，文档那一侧由 `s24` 每次发版跑一次（E123）。

### 生成物与生成它的配方不能有两份（`s25`，310 §12.2、§20 A25）

310 §12.2 只要求一件事：CI 里重新生成、再 diff；§20 A25 从另一侧说同一句话（不修改工作区、能发现缺失/多余/不一致）。
这两句原先唯一的实现者是 `make swagger-check`，而**没有任何读者跑它**——`internal/docs/` 的三个文件只被 blank import 读，
blank import 对字节没有要求。同一个形状在 304 里出现过两次（E120 的 `--list` 是第二份手写清单、E130 的「已闭环（E119）」
是没有读者的留言）：检查存在、仪式存在，接缝不存在。

```bash
bash scripts/release-gate/openapi.sh             # 门禁那一行（一次生成，~10s 热缓存）
bash scripts/release-gate/openapi.sh --selftest  # 6 条变异，含两个方向的漂移
bash scripts/release-gate/openapi.sh gen internal/docs   # `make swagger` 现在调的就是这一句
```

* 版本、flags、三个文件名**只在脚本里写一次**，`Makefile` 的 `swagger`/`swagger-check` 只是它的别名——两份配方漂移的那天，
  「没有差异」会同时报告两个不同的事实。
* `check` 每次**真生成**到临时目录，并断言生成物 mtime 晚于本次启动：`OAPI_SKIP_GEN=1` 必须被拒（第 6 条变异）。
  一个读不到生成结果的「无差异」，比没有检查更坏。
* **基线是索引，不是工作区**（`git show :internal/docs/<f>`）：索引就是下一次提交会带的东西，也是 CI 检出的东西；
  索引里没有就红，并打印 `git add internal/docs` 那一行。读工作区会让「无漂移」变成关于某一台机器的读数——这条行曾经绿着，
  而它比较的那三个文件从未入过库（304 §4 E140）。
* `gen` 先写本次运行自己的暂存目录，三个文件齐了才复制：**配方替换它生成的东西，不删除它不生成的东西**。旧实现以
  `rm -rf` 开头，一次失败生成清空了 `internal/docs/`，连带删掉手写的 `envelope.go` 与 `spec.go`。
* 工作区必须前后一致（`git status --porcelain internal/docs`）：检查不得顺手修好被测物，否则它分不清漂移和修复。
* 只 diff 一个方向会**批准删除**，所以变异 ② 与 ③ 是两个方向。
* 它不判路由覆盖（那是 `openapi_coverage_test.go`，跑在 `s07` 里），也不声称离线可用（无缓存无网络 = exit 2）。

### 兼容性是「少了什么」，不是「变了什么」（`s26`，310 Phase 3、§20 A21）

`openapi_coverage_test.go` 问的是**同一时刻**路由与规格是否互相覆盖，`s25` 问的是**同一份字节**的两个来源是否相同。
在 `s26` 之前，**没有一条行比较过两个时间点**——于是删一个路径、改一个 operationId、把可选参数变成必填，都能一路绿灯到发版。

```bash
bash scripts/release-gate/openapi-breaking.sh                 # 门禁那一行：索引 vs 最近带产物的 tag
bash scripts/release-gate/openapi-breaking.sh check <ref>     # 对着指定的 ref（演练用）
bash scripts/release-gate/openapi-breaking.sh --selftest      # 13 例：7 条规则各自红、增长绿、4 种豁免形状、空侧面红
```

* 七条**损失**规则：R1 路径、R2 操作、R3 operationId、R4 新增必填参数、R5 文档化响应码、R6 枚举变窄、R7 schema 类型。
  反向（新路径、可选参数、新响应码、更宽枚举）一律安静：**一条会因为正常发版变红的门禁，最后一定会被关掉**。
* 当前侧是**索引**（E140 的同一决定），基线侧是 `git cat-file -e <tag>:internal/docs/swagger.json` 由新到旧找到的第一个 tag。
  两侧的路径/操作数都打印：`0 findings` 与 `0 operations read` 不能长得一样。
* **没有基线时不沉默**：`baseline tag: none — examined 9 tag(s)` + `0 released operations`（已知的零，不是「测不到」）。
  第一个带上规格的 tag 会把同一段代码变成真比较——悬崖两侧都写在输出里。
* 豁免在 `openapi-breaking-allow.txt`：`RULE METHOD PATH  reason`；理由短于 12 字红，匹配不到任何 finding 也红（E135），
  同一损失写了第二条理由同样红（否则「最后一条胜出」会让另一条理由从记录里消失），
  被豁免的 finding 仍以 `exempt …(reason)` 打印——门禁展示它放过了什么，而不是忘掉它。
* 顺带修掉 `s25` 的读数：`grep -c '"operationId"'` 报 622，真实操作数 616（多出的是路径参数名与描述串）。


### 读数必须说出它读到了什么（`s27`，301 GW-2、304 §4 E151）

`scripts/gateway-perf-baseline.py` 是 T14 的仪器。它的 `--selftest` 从 E92 起就存在，而 `grep -rn gateway-perf-baseline
scripts/release-gate*.sh scripts/release-gate/*.sh Makefile .github/workflows/*.yml` **一直是空的**：仪器会自己报红，
但没人敲它。这就是 E120/E130 的失败形态，只是发生在「生成仪器的脚本」上而不是「生成的产物」上。

它同时有两种读者需要的、以前没有的诚实：

* **`unverified` 不是一个数。** `samples 5 < 20` 这一句曾经同时被三种事实用：主动跳过（`--skip-live`）、我们只发了 5 个
  （`sample_plan`）、发完了但被上游 429 挡回（`upstream_quota`）。第三种是「上游不让测完」，重跑没用，要的是配额
  或换池；写成前一种会让人得出「明天再跑一次」的排期结论（§4 E146 的「三种空」在延迟表上的同一句）。
* **格 id 里的 `|` 不是列分隔符。** 原样入表会让每行多出 4 列——一张读不出来的表没有了读者，比没有表更坏。

```bash
python scripts/gateway-perf-baseline.py --selftest      # 43 断言：含"两种 shortfall 不得印同一串"这类否定式
python E:/tmpgo/replay/e151_mut.py                      # 7 变异全 KILLED（配额改标签/重试 429 不计数/分族合一/同串/paced 不标注/表宽/家族行不渲染）
```

* 判定逻辑一个字没动：`blocked_by` 与 `paced_by_quota` 都不参与 verdict，c2/c4 六格仍是未测（要 §7 第 7 条的批准）。
  这条门禁证明的是**仪器会把三种不足分开印、并且在被改坏时会红**，不证明网关的并发扩展性。
* 门禁里这是一行 `static`（`s27.perf-selftest`），跟着 `s26` 跑；`--list` 里与其它行一样自动出现，因为它就是从声明处读的。


### 选集本身就是被检查的面（`--only`/`--skip`，304 §4 E98/E102）

一条 check 没跑，和一条 check 跑了但没断言，在报告里长得一样——所以选集不再只是过滤器：

* id 集合**从 `release-gate.sh` 里 `run_check`/`run_suite` 的声明处读**（`known_ids()` 扫脚本自身），不第二次手写，新加 check 自动已知；
* `--only` 里任何一个 token 不在集合内 → **exit 2**，并把全部 id 打印出来。前缀不是简写：`--only=s16` 曾经一个都没选中，于是每条 check 落 `SKIP`、`required` 成 `0/0`、顶行打 `PASS`；
* `--skip` 的未知 token 同样 exit 2 —— 那个方向更安静：「以为排除了，其实还在跑」；
* `--only X --skip X` 这类**空选集 → exit 2**；
* 同一个旗子可以给多次，语义是**并集**（`--only a --only b` 选两条、`--skip a --skip b` 排除两条）。第一版是赋值，`--skip a --skip b` 静默只排除了 `b`（304 §4 E114）；
* 最后一道闸：`RUNS=0` 一律 exit 2，**绝不落 `PASS`**。

`selftest.sh` 有 6 例选集用例，并且 `--selftest` 会把四条规则各删一条造 4 个变异体，要求用例**全部变红**（`SELFTEST OK: 4 gate mutants refused`）——一条只为好看而存在的检查，本身就是这个门禁要防的东西。

### 静态：count 与 read 不能共用一个 builder（`s20`）

知识运营看板的 500 是这样一个形状：`q.Group("id")` 一条裸调用改掉了分页 builder，
count 变成 `count(*) ... GROUP BY id`（对），读取变成 `SELECT * ... GROUP BY id`
（PostgreSQL 42803）。SQLite 收下后一句，所以基于它的单测全绿。

`s07` 里的 PG 契约测试能抓它，但只在设了 `RGX_TEST_POSTGRES_DSN` 时、且代码已经能编译时。
`s20.gorm-builders`（`cmd/gormguard`，只用标准库 `go/ast`）把同一条规则前移到静态层：
同一个 builder 既做 `Count` 又做读取，并带着 `Group/Select/Distinct/Having/Unscoped`
就 **FAIL**；只共享 `Joins` 时是**警告**（count 与 read 的行倍增一致，通常是有意的），
同一 builder 再加分组子句才升级为错误。`Session(&gorm.Session{})` 隔离的 count 和
`countQuery`/`listQuery` 双 builder 都算合规。`make check-gorm-builders` 与 CI 的
`go-build` job 跑的是同一份命令。

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
