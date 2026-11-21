# 预注册文件（doc/301 §9.2，T05）

这个目录放**先于运行**提交的实验预注册文件。`goldtool prereg --freeze` 生成，`goldtool prereg
--check FILE` 校验；`goldtool eval --prereg FILE` 会把一次评价绑到某一份冻结规则上。

- `prereg-e1-v1.json` —— E1 方法选型（`unsupported_false_positive_rate` 主指标、4 个臂、
  15 个阈值、主样本 `primary-random-20261121`）。冻结于 2026-11-21，早于 T02 金标标注。

## 为什么这里只有哈希

文件里记的是输入文件的 **sha256 + 行数**，不是文件本身：主样本（120 条）与候选框（371 条）
含客户原文，只存在于工作站 `tmp/gold/`（`.gitignore:82`），随 `goldtool sample --seed` 可重建。

因此在**有样本的机器**上 `--check` 会重新哈希并判绿；在**只有仓库**的机器上它会说
`cannot verify: input sample (...) is not present here` 并以 **exit 3** 结束。exit 3 是刻意的：
「我没法校验」和「我校验过了，成立」不是同一句话。

## 换阈值就是换预注册

`--check` 会把文件里的 15 个阈值逐个与 `cmd/goldtool/eval.go` 里的常量比对，**双向**：
改了常量不改文件、或改了文件不改常量，都是红色。要动阈值只有两条路——补样本，或者冻结
`prereg-e1-v2.json` 让差异进 review。冻结文件用 `O_EXCL` 写，不会被原地覆盖。
