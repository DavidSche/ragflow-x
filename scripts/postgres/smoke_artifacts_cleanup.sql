-- 冒烟副作用清理（C3.4 / T7.3′ 端到端冒烟，2026-10-26 在共享开发库 192.168.4.153/ragflow-x 产生）
-- 裁决：doc/239 R-04 = A（清理）。执行方式：scripts/pg_smoke_artifacts_cleanup.ps1 -Execute
--
-- 边界（重要）：
--   1. 只删本表列出的 4 类业务工件，不删 rgx_audit_log —— 审计是 hash 链（prev_hash/hash/seq），
--      删行会让 GET /api/v1/audit/verify 变成 false；审计里对这 4 个 id 的 4 条引用必须保留为证据。
--   2. 不碰 RAGFlow 侧库本体 a5e790a98156462780ac5a0ec11b5051（C 臂真实数据，40 个文档）。
--   3. 待删的 RAGFlow 侧空库 fb92ee2929244752b30ca09f1d223a51（c34-smoke-ds，0 文档）由
--      RAGFlow API 单独删除，不在本脚本内（数据库侧只删链接行）。
--   4. 全部语句幂等：重复执行第二次影响 0 行。

BEGIN;

-- 预检：应当命中 2 / 1 / 1 / 2 / 4 / 4 / 4 行（dataset_link / chat / trial_run / case_result / approval / step / actor）
SELECT 'pre_dataset_link' AS check, count(*) FROM rgx_dataset_link
 WHERE id IN ('916beee8d35a4596aba78141c9c46383', 'c34smokedataset0000000000000001');
SELECT 'pre_chat' AS check, count(*) FROM rgx_chat
 WHERE id = '0550161dd4df45f08a21391b673a1c44';
SELECT 'pre_trial_run' AS check, count(*) FROM rgx_trial_run
 WHERE id = '09506993c97a44729f95c16c539197ed';
SELECT 'pre_case_result' AS check, count(*) FROM rgx_trial_case_result
 WHERE run_id = '09506993c97a44729f95c16c539197ed';

-- 1) 试跑结果 → 试跑
DELETE FROM rgx_trial_case_result WHERE run_id = '09506993c97a44729f95c16c539197ed';
DELETE FROM rgx_trial_run WHERE id = '09506993c97a44729f95c16c539197ed';

-- 2) 审批（含执行失败的重复申请 c858b432…）：先子表后主表
DELETE FROM rgx_approval_step_actor WHERE approval_id IN (
  '2f94875ca9be477b81604478a7613b72', '0e4eb936feb1431994032f1e1dab75d3',
  'af21ce72ed7e40fa9013b4b9fec960c1', 'c858b4320b02498a9c5e15ad7d040913');
DELETE FROM rgx_approval_step WHERE approval_id IN (
  '2f94875ca9be477b81604478a7613b72', '0e4eb936feb1431994032f1e1dab75d3',
  'af21ce72ed7e40fa9013b4b9fec960c1', 'c858b4320b02498a9c5e15ad7d040913');
DELETE FROM rgx_approval WHERE id IN (
  '2f94875ca9be477b81604478a7613b72', '0e4eb936feb1431994032f1e1dab75d3',
  'af21ce72ed7e40fa9013b4b9fec960c1', 'c858b4320b02498a9c5e15ad7d040913');

-- 3) 助手（带模板溯源的冒烟助手）
DELETE FROM rgx_chat WHERE id = '0550161dd4df45f08a21391b673a1c44';

-- 4) 数据集链接行（含人工插入的 c34-smoke-c-arm；只删链接，不动 RAGFlow 库）
DELETE FROM rgx_dataset_link WHERE id IN (
  '916beee8d35a4596aba78141c9c46383', 'c34smokedataset0000000000000001');

-- 后置检查：全部应为 0
SELECT 'post_remaining' AS check,
       (SELECT count(*) FROM rgx_dataset_link WHERE id IN ('916beee8d35a4596aba78141c9c46383', 'c34smokedataset0000000000000001'))
     + (SELECT count(*) FROM rgx_chat WHERE id = '0550161dd4df45f08a21391b673a1c44')
     + (SELECT count(*) FROM rgx_trial_run WHERE id = '09506993c97a44729f95c16c539197ed')
     + (SELECT count(*) FROM rgx_trial_case_result WHERE run_id = '09506993c97a44729f95c16c539197ed') AS remaining;

-- 审计必须保持完整：这 4 条引用不删，链校验仍为 true
SELECT 'audit_refs_kept' AS check, count(*) FROM rgx_audit_log
 WHERE resource_id IN ('0550161dd4df45f08a21391b673a1c44', '916beee8d35a4596aba78141c9c46383',
                       'c34smokedataset0000000000000001', '09506993c97a44729f95c16c539197ed');

COMMIT;
