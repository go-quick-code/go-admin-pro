package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/iploc"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 地区（issue #82，spec §3.9/§4.8/§5.7）----------

// ErrAreaImportPlatformOnly 是地区导入越权访问的哨兵错误（非平台租户 403）。
var ErrAreaImportPlatformOnly = errors.New("地区数据仅平台租户可导入")

// AreaImportMaxBatch 是单次导入的最大条数（spec §4.8.2：单次最多 5000 条）。
const AreaImportMaxBatch = 5000

// AreaTree 返回嵌套地区树：parentCode 为空返回全量树（顶层省级），
// 指定编码返回以其子级为根的子树。区县未导入时市级即为叶子。
//
// 读取开放给所有登录用户（spec §4.8.1）：路由层不设 RequirePermission，
// 与部门树同口径；菜单 113 持 system:area:query 仅控制页面入口可见性。
func (s *Service) AreaTree(ctx context.Context, parentCode string) ([]*dto.AreaNode, error) {
	all, err := s.repo.ListAllAreas(ctx)
	if err != nil {
		return nil, err
	}
	// 一次性载入后内存组树（省市 + 常见区县量级在数千行内，避免逐层 N+1）。
	byParent := make(map[string][]*dto.AreaNode, len(all))
	for i := range all {
		a := &all[i]
		byParent[a.ParentCode] = append(byParent[a.ParentCode], &dto.AreaNode{
			ID: a.ID, Code: a.Code, ParentCode: a.ParentCode, Name: a.Name,
			Level: a.Level, Sort: a.Sort, Status: a.Status, Children: []*dto.AreaNode{},
		})
	}
	// 组树：每个节点的 children 取 byParent[code]（列表已按 sort, code 有序）。
	// 先铺满 children 再从根裁剪：只保留可达的节点作为返回值。
	for _, kids := range byParent {
		for _, n := range kids {
			if grandKids, ok := byParent[n.Code]; ok {
				n.Children = grandKids
			}
		}
	}
	if kids, ok := byParent[parentCode]; ok {
		return kids, nil
	}
	// 父编码不存在（或无启用子级）：返回空树而非错误，调用方按空展示。
	return []*dto.AreaNode{}, nil
}

// AreaChildren 返回 parentCode 的直接子级（平铺）；parentCode 为空返回省级
// （spec §4.8.3）。仅启用行，按 sort、code 升序。
func (s *Service) AreaChildren(ctx context.Context, parentCode string) ([]model.Area, error) {
	return s.repo.ListAreasByParent(ctx, parentCode)
}

// ImportAreas 平台导入（spec §4.8.2）：JSON 数组按 code upsert。
//   - 仅平台租户（ID=1）可导入（spec §4.8.1），否则 403；
//   - 单次最多 AreaImportMaxBatch 条，空数组与超限一律 400；
//   - 条目校验：code/name 必填（code ≤ 12 字符、name ≤ 100 字符）、
//     level ∈ {1,2,3}、level 1 不允许有父编码、level 2/3 的父节点必须在
//     库中已存在或同批出现；
//   - 同批 code 重复时后条覆盖前条（与 MySQL upsert 语义一致，确定性取舍）；
//   - 返回实际写入（去重后）的条数。
func (s *Service) ImportAreas(ctx context.Context, items []dto.AreaImportItem) (int, error) {
	if tenant.ID(ctx) != tenant.DefaultID {
		return 0, xerr.Wrap(ErrAreaImportPlatformOnly, http.StatusForbidden, http.StatusForbidden, ErrAreaImportPlatformOnly.Error())
	}
	if len(items) == 0 {
		return 0, xerr.BadRequest("导入数据不能为空")
	}
	if len(items) > AreaImportMaxBatch {
		return 0, xerr.BadRequest(fmt.Sprintf("单次最多导入 %d 条，本次 %d 条", AreaImportMaxBatch, len(items)))
	}

	// 去重（后条覆盖）并逐条校验字段本身。
	areas := make([]model.Area, 0, len(items))
	byCode := make(map[string]int, len(items))
	for i, it := range items {
		if it.Code == "" || len(it.Code) > 12 {
			return 0, xerr.BadRequest(fmt.Sprintf("第 %d 条：code 必填且不超过 12 字符", i+1))
		}
		if it.Name == "" || len(it.Name) > 100 {
			return 0, xerr.BadRequest(fmt.Sprintf("第 %d 条（%s）：name 必填且不超过 100 字符", i+1, it.Code))
		}
		if it.Level < 1 || it.Level > 3 {
			return 0, xerr.BadRequest(fmt.Sprintf("第 %d 条（%s）：level 必须为 1/2/3", i+1, it.Code))
		}
		if it.Level == 1 && it.ParentCode != "" {
			return 0, xerr.BadRequest(fmt.Sprintf("第 %d 条（%s）：省级不允许携带父编码", i+1, it.Code))
		}
		if it.Level > 1 && it.ParentCode == "" {
			return 0, xerr.BadRequest(fmt.Sprintf("第 %d 条（%s）：非省级必须指定父编码", i+1, it.Code))
		}
		status := int8(model.StatusEnable)
		if it.Status != nil {
			status = *it.Status
		}
		a := model.Area{
			Code: it.Code, ParentCode: it.ParentCode, Name: it.Name,
			Level: it.Level, Sort: it.Sort, Status: status,
		}
		if prev, dup := byCode[it.Code]; dup {
			areas[prev] = a
		} else {
			byCode[it.Code] = len(areas)
			areas = append(areas, a)
		}
	}

	// 父节点存在性校验（spec §4.8.2「父节点必须已存在（或同批出现）」）：
	// 收集不在本批中的父编码，一次 IN 查询核对。
	batchCodes := make(map[string]struct{}, len(areas))
	for _, a := range areas {
		batchCodes[a.Code] = struct{}{}
	}
	parentCodes := make([]string, 0, len(areas))
	seenParent := make(map[string]struct{})
	for _, a := range areas {
		if a.ParentCode == "" {
			continue
		}
		if _, sameBatch := batchCodes[a.ParentCode]; sameBatch {
			continue
		}
		if _, dup := seenParent[a.ParentCode]; dup {
			continue
		}
		seenParent[a.ParentCode] = struct{}{}
		parentCodes = append(parentCodes, a.ParentCode)
	}
	if len(parentCodes) > 0 {
		dbAreas, err := s.repo.GetAreasByCodes(ctx, parentCodes)
		if err != nil {
			return 0, err
		}
		missing := make([]string, 0)
		for _, a := range areas {
			if a.ParentCode == "" {
				continue
			}
			if _, sameBatch := batchCodes[a.ParentCode]; sameBatch {
				continue
			}
			if _, ok := dbAreas[a.ParentCode]; !ok {
				missing = append(missing, fmt.Sprintf("%s（条目 %s）", a.ParentCode, a.Code))
			}
		}
		if len(missing) > 0 {
			return 0, xerr.BadRequest("父节点不存在，需要先导入上级区划或与本批同批提交：" + strings.Join(missing, "、"))
		}
	}

	if err := s.repo.UpsertAreas(ctx, areas); err != nil {
		return 0, err
	}
	return len(areas), nil
}

// SetIPResolver 注入 IP 归属地解析器（internal/app 装配时调用）。
// nil 表示未启用，登录日志归属地为空。
func (s *Service) SetIPResolver(r iploc.Resolver) {
	s.ipResolver = r
}
