-- +goose Up
-- 公海自动回收与租户配置（issue #14）：租户级公海配置表。
-- docs/crm-mvp-spec.md §4.2：自动回收条件（有负责人、last_follow_time 超过
-- N 天默认 30、无未作废合同）与认领上限按租户配置；表结构与默认值随本迁移建齐。
--
-- 每租户一行（tenant_id 唯一）。行缺失（新建租户尚未配置）时业务层按默认值
-- 执行（recycle_days=30 / claim_limit=50），首次编辑才落行——因此本迁移
-- 不做批量数据初始化，租户 1 的默认行由 internal/seed 按需写入（与字典、
-- 参数等种子数据同一管理路径）。
--
-- 未作废合同的排除不建表也不留列：合同实体与作废状态由 #16 落地
-- （迁移号顺延 00009+），本工单通过依赖接口 ContractChecker 注入
-- （internal/module/crm/service/pool_recycle_rules.go），表结构无需预埋。

CREATE TABLE `crm_pool_config` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID，每租户一行',
  `recycle_days` int NOT NULL DEFAULT '30' COMMENT '自动回收天数：last_follow_time 超过该天数回收（NULL 回退 created_at）',
  `claim_limit` int NOT NULL DEFAULT '50' COMMENT '认领上限：名下未删除客户总数达上限拒绝认领',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_crm_pool_config_tenant` (`tenant_id`),
  KEY `idx_crm_pool_config_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的配置表。00008 未引入其他结构或数据变更。
DROP TABLE IF EXISTS `crm_pool_config`;
