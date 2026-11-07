package service

import "context"

// ---------- Excel 导出展示名补齐（issue #96，spec §4.11）----------
// 单据导出的负责人/销售员列与 CRM 导出约束体系一致输出人名而非裸 ID；
// 批量解析一次，缺失 ID（用户已删除/跨租户）在导出列留空。

// userNicknames 批量解析导出列所需的用户昵称（去重后一次查询）。
func (s *Service) userNicknames(ctx context.Context, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	seen := make(map[int64]struct{}, len(ids))
	uniq := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return map[int64]string{}, nil
	}
	return s.sys.UserNicknames(ctx, uniq)
}

// collectOwnerIDs 收集导出行的 owner 用户 ID（userNicknames 内部去重）。
func collectOwnerIDs[T any](list []T, owner func(T) int64) []int64 {
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, owner(row))
	}
	return ids
}
