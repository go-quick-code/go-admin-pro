-- +goose Up
-- 行政区划参考数据（issue #82，docs/platform-mvp-spec.md §3.9/§4.8）：
-- 省市两级由种子写入（go:embed JSON，表为空时幂等写入），区县经
-- POST /system/area/import 按 code upsert 补齐；树接口按 parent_code 下钻。
--
-- 平台级参考数据（同 system_menu）：不登记 TenantTables，所有登录用户
-- 可读（spec §4.8.1），写入仅平台租户（service 层校验）。
-- 参考数据不设软删与 creator/updater（spec §3.9），因此也不复用
-- BaseEntity 底座，模型侧用精简结构（internal/module/system/model/area.go）。

CREATE TABLE `system_area` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL COMMENT '创建时间',
  `updated_at` datetime(3) DEFAULT NULL COMMENT '更新时间',
  `code` varchar(12) NOT NULL COMMENT '行政区划代码（GB/T 2260，按 code upsert）',
  `parent_code` varchar(12) NOT NULL DEFAULT '' COMMENT '父级区划代码（省级为空串；下级必须存在）',
  `name` varchar(100) NOT NULL COMMENT '名称',
  `level` tinyint NOT NULL DEFAULT '1' COMMENT '层级：1 省 / 2 市 / 3 区县',
  `sort` int NOT NULL DEFAULT '0' COMMENT '排序（同级内升序）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_system_area_code` (`code`),
  KEY `idx_system_area_parent_code` (`parent_code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='行政区划（平台级参考数据）';

-- +goose Down
DROP TABLE IF EXISTS `system_area`;
