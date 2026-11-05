-- +goose Up
-- 合同附件（issue #17，docs/crm-mvp-spec.md §6 合同管理「附件」/§9「附件复用 infra_file」）：
-- 不新增表，只给既有 infra_file 增加通用业务归属列，任何业务都可按
-- (biz_type, biz_id) 挂载文件；合同附件写入 biz_type='crm_contract'、biz_id=合同 ID。
-- 未关联业务的既有文件保持空串/0 默认值，语义不变。索引与租户过滤列同序，
-- 列表/归属校验查询走 (tenant_id, biz_type, biz_id) 索引。
ALTER TABLE `infra_file`
  ADD COLUMN `biz_type` varchar(32) NOT NULL DEFAULT '' COMMENT '业务类型（如 crm_contract；空串表示未关联业务）' AFTER `provider`,
  ADD COLUMN `biz_id` bigint NOT NULL DEFAULT '0' COMMENT '业务 ID（与 biz_type 组成归属键；0 表示未关联业务）' AFTER `biz_type`,
  ADD KEY `idx_infra_file_biz` (`tenant_id`,`biz_type`,`biz_id`);

-- +goose Down
ALTER TABLE `infra_file`
  DROP KEY `idx_infra_file_biz`,
  DROP COLUMN `biz_id`,
  DROP COLUMN `biz_type`;
