-- +goose Up
-- 站内通知（issue #45，docs/oa-mvp-spec.md §3.2 notify / §4.13 通知）：
-- 通知按接收人物化一行（不做广播表），由 bpm/公告/会议室等生产者在触发事件的
-- 同一事务内批量写入（spec §4.13：写失败则源操作回滚，事务句柄经 notify
-- service 的 CreateInTx seam 传入）。类型枚举 bpm_todo/bpm_result/announcement/
-- meeting_reminder；read_at 为 NULL 表示未读，未读数走 (tenant_id, user_id,
-- read_at) 索引前缀；列表按 id 倒序走 (tenant_id, user_id, id) 索引。
-- (tenant_id, biz_type, biz_id) 为 spec §4.13 预留的「任务办理后标记对应
-- bpm_todo 已读」检索路径（#46 接入时无需再改表）。

CREATE TABLE `notify_message` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint NOT NULL COMMENT '接收人用户 ID（按接收人物化，一人一行）',
  `type` varchar(32) NOT NULL COMMENT '类型：bpm_todo/bpm_result/announcement/meeting_reminder',
  `title` varchar(128) NOT NULL COMMENT '标题',
  `content` varchar(500) DEFAULT NULL COMMENT '内容摘要',
  `biz_type` varchar(64) DEFAULT NULL COMMENT '业务类型（如 bpm_task/oa_announcement/oa_room_booking），与 biz_id 一起定位业务',
  `biz_id` bigint NOT NULL DEFAULT '0' COMMENT '业务 ID',
  `route` varchar(255) DEFAULT NULL COMMENT '前端跳转路由 path（为空表示仅站内展示，不跳转）',
  `route_params` varchar(512) DEFAULT NULL COMMENT '跳转参数 JSON 对象字符串（如 {"taskId":1}），与 route 一起构成跳转目标',
  `read_at` datetime(3) DEFAULT NULL COMMENT '已读时间；NULL 表示未读',
  PRIMARY KEY (`id`),
  KEY `idx_notify_message_tenant_user_read_id` (`tenant_id`,`user_id`,`read_at`,`id`),
  KEY `idx_notify_message_tenant_user_id` (`tenant_id`,`user_id`,`id`),
  KEY `idx_notify_message_tenant_biz` (`tenant_id`,`biz_type`,`biz_id`),
  KEY `idx_notify_message_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `notify_message`;
