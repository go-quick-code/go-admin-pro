//go:build integration

// 字典项引用保护的 HTTP 集成用例（issue #64 code-review P1，spec §4.14）：
// 报销类别被 oa_expense_item 引用时只能停用不能删除——
//   - 有引用 → DELETE 409（提示改为停用）；
//   - 停用该字典项后仍被历史明细引用 → DELETE 仍 409；
//   - 未被引用的字典项仍可直接删除。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（与 expense 用例同库，
// 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// dictDataItem 是字典数据列表的最小解析结构。
type dictDataItem struct {
	ID       int64  `json:"id"`
	DictType string `json:"dictType"`
	Label    string `json:"label"`
	Value    string `json:"value"`
	Sort     int    `json:"sort"`
	Status   int8   `json:"status"`
}

func TestIntegrationDictDataUsageGuard(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env
	admin := env.Login(t, "admin", "123456")

	// 找到 oa_expense_category 的「交通」字典项。
	status, api := env.Request(t, http.MethodGet,
		"/system/dict-data/list?dictType=oa_expense_category", admin, nil)
	mustOK(t, "读取报销类别字典", status, api)
	var dictRows []dictDataItem
	if err := json.Unmarshal(api.Data, &dictRows); err != nil {
		t.Fatalf("解析字典数据: %v（data=%s）", err, string(api.Data))
	}
	var traffic dictDataItem
	for _, row := range dictRows {
		if row.Value == "交通" {
			traffic = row
			break
		}
	}
	if traffic.ID <= 0 {
		t.Fatalf("种子字典缺少「交通」项：%+v", dictRows)
	}

	// 草稿报销单引用「交通」类别 → 字典项被引用。
	createExpenseClaim(t, env, exp.employee, "字典引用用例")
	status, api = env.Request(t, http.MethodDelete,
		fmt.Sprintf("/system/dict-data/%d", traffic.ID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("被引用字典项删除应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if api.Msg == "" {
		t.Fatalf("409 应带可读提示（改为停用）")
	}

	// 停用后历史明细仍引用 → 删除仍 409。
	status, api = env.Request(t, http.MethodPut, "/system/dict-data", admin, map[string]any{
		"id": traffic.ID, "dictType": traffic.DictType, "label": traffic.Label,
		"value": traffic.Value, "sort": traffic.Sort, "status": 1,
	})
	mustOK(t, "停用报销类别字典项", status, api)
	status, api = env.Request(t, http.MethodDelete,
		fmt.Sprintf("/system/dict-data/%d", traffic.ID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("停用后仍被引用，删除应 409，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 未被引用的字典项可删除（新建「其他2」后直接删除）。
	status, api = env.Request(t, http.MethodPost, "/system/dict-data", admin, map[string]any{
		"dictType": "oa_expense_category", "label": "其他2", "value": "其他2",
		"sort": 99, "status": 0,
	})
	mustOK(t, "创建未引用字典项", status, api)
	status, api = env.Request(t, http.MethodGet,
		"/system/dict-data/list?dictType=oa_expense_category", admin, nil)
	mustOK(t, "重读报销类别字典", status, api)
	dictRows = nil
	if err := json.Unmarshal(api.Data, &dictRows); err != nil {
		t.Fatalf("解析字典数据: %v", err)
	}
	var unused dictDataItem
	for _, row := range dictRows {
		if row.Value == "其他2" {
			unused = row
			break
		}
	}
	if unused.ID <= 0 {
		t.Fatalf("新建字典项未出现在列表：%+v", dictRows)
	}
	status, api = env.Request(t, http.MethodDelete,
		fmt.Sprintf("/system/dict-data/%d", unused.ID), admin, nil)
	mustOK(t, "未被引用的字典项可删除", status, api)
}
