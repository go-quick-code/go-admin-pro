-- +goose Up
-- 数据范围对齐芋道 1–5（issue #5，见 ADR 0004）：
--   旧枚举 1 全部 / 2 本部门及以下 / 3 本部门 / 4 仅本人
--   新枚举 1 全部 / 2 自定义部门 / 3 本部门 / 4 本部门及以下 / 5 仅本人
-- 重映射：2→4、4→5；1/3 不动。用单条 CASE 同时映射各分支：
-- 每行按改前旧值定位目标，避免「先 4→5 再 2→4」顺序执行时把已改写的行二次重映射。
-- 自定义部门（system_role_dept）只在新库写入；存量库升级后无自定义配置，
-- 原本按旧枚举工作的角色语义经重映射保持不变。

UPDATE `system_role` SET `data_scope` = CASE
  WHEN `data_scope` = 2 THEN 4  -- 旧「本部门及以下」→ 4
  WHEN `data_scope` = 4 THEN 5  -- 旧「仅本人」→ 5
  ELSE `data_scope`             -- 1 全部、3 本部门 不动
END;

-- 角色自定义部门关联：复合主键 + tenant_id，不设软删除，变更由操作日志记录（ADR 0002）。
CREATE TABLE `system_role_dept` (
  `role_id` bigint NOT NULL,
  `dept_id` bigint NOT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  PRIMARY KEY (`role_id`,`dept_id`),
  KEY `idx_system_role_dept_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除关联表；data_scope 按改前新值反向映射（同样单条 CASE，按原值定位分支）：
--   4（本部门及以下）→ 旧 2；5（仅本人）→ 旧 4；
--   2（自定义部门）在旧枚举中无对应语义，按最严格原则回退旧「仅本人」（fail-closed）。

DROP TABLE IF EXISTS `system_role_dept`;

UPDATE `system_role` SET `data_scope` = CASE
  WHEN `data_scope` = 4 THEN 2  -- 新「本部门及以下」→ 旧 2
  WHEN `data_scope` = 5 THEN 4  -- 新「仅本人」→ 旧 4
  WHEN `data_scope` = 2 THEN 4  -- 新「自定义部门」→ 旧「仅本人」（fail-closed）
  ELSE `data_scope`
END;
