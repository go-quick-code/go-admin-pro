// document_rule_test.go 是文档 ACL 判定规则的表驱动测试（issue #58，
// docs/oa-mvp-spec.md §4.12）。锁定：
//   - 读权限取「最近一个有显式 ACL 的祖先目录」（含自身），子目录覆盖父目录
//     而不是叠加；
//   - 链上没有显式 ACL 时只有 owner / oa:document:manage 可见；
//   - 四类主体（全员/部门子树/角色/用户）的命中与不命中；
//   - owner 与管理员旁路；
//   - ACL 归一化（去重/排序/非法值）与顶层目录必须显式 ACL；
//   - 非空目录禁止删除（409）。
package service

import (
	"errors"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// docFolder 构造测试目录（只填判定需要的字段）。
func docFolder(id, parentID, ownerID int64) model.DocumentFolder {
	return model.DocumentFolder{
		BaseEntity: model.BaseEntity{ID: id},
		ParentID:   parentID,
		OwnerID:    ownerID,
	}
}

// docFolderMap 把目录列表转成按 ID 索引的 map。
func docFolderMap(rows ...model.DocumentFolder) map[int64]model.DocumentFolder {
	out := make(map[int64]model.DocumentFolder, len(rows))
	for _, row := range rows {
		out[row.ID] = row
	}
	return out
}

// docACL 构造一条 ACL。
func docACL(targetType int8, targetID int64) DocumentACLTarget {
	return DocumentACLTarget{TargetType: targetType, TargetID: targetID}
}

// docViewer 构造查看者（部门/角色预置）。
func docViewer(userID int64, depts, roles []int64, manage bool) DocumentViewer {
	return DocumentViewerOf(userID, depts, roles, manage)
}

// TestResolveEffectiveDocumentACL 锁定最近祖先解析与子覆盖父。
func TestResolveEffectiveDocumentACL(t *testing.T) {
	aclAll := []DocumentACLTarget{docACL(model.DocumentACLTargetAll, 0)}
	aclDept102 := []DocumentACLTarget{docACL(model.DocumentACLTargetDept, 102)}

	// A(全员) → B(无 ACL) → C(无 ACL)：C 继承 A。
	// A(全员) → B(部门 102) → C(无 ACL)：C 继承 B，B 覆盖 A。
	// 无 ACL 顶层：D(无 ACL)。
	// 自环（A 的 parent 指向 B，B 的 parent 指向 A）不会死循环。
	folders := docFolderMap(
		docFolder(1, 0, 10), // A
		docFolder(2, 1, 10), // B
		docFolder(3, 2, 11), // C
		docFolder(4, 2, 12), // C2（B 的另一个子目录）
		docFolder(5, 0, 13), // D（无 ACL 顶层）
		docFolder(6, 7, 14), // E（环 A，带 ACL）
		docFolder(7, 6, 14), // F（环 B，带 ACL）
		docFolder(8, 9, 15), // G（环 C，无 ACL）
		docFolder(9, 8, 15), // H（环 D，无 ACL）
	)
	aclByFolder := map[int64][]DocumentACLTarget{
		1: aclAll,
		2: aclDept102,
		6: aclAll,
	}

	cases := []struct {
		name       string
		folderID   int64
		wantOK     bool
		wantSource int64
		wantOwner  int64
		wantExp    bool
		wantACL    []DocumentACLTarget
	}{
		{name: "无 ACL 顶层", folderID: 5, wantOK: true, wantSource: 0, wantOwner: 13},
		{name: "自生效（有显式 ACL）", folderID: 1, wantOK: true, wantSource: 1, wantOwner: 10, wantExp: true, wantACL: aclAll},
		{name: "最近祖先命中（跳过无 ACL 中间层）", folderID: 3, wantOK: true, wantSource: 2, wantOwner: 11, wantACL: aclDept102},
		{name: "子目录覆盖父目录", folderID: 2, wantOK: true, wantSource: 2, wantOwner: 10, wantExp: true, wantACL: aclDept102},
		{name: "不存在的目录", folderID: 99, wantOK: false},
		{name: "环不会死循环（有 ACL）", folderID: 6, wantOK: true, wantSource: 6, wantOwner: 14, wantExp: true, wantACL: aclAll},
		{name: "环不会死循环（无 ACL，解析到梯度上限）", folderID: 8, wantOK: true, wantSource: 0, wantOwner: 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveEffectiveDocumentACL(tc.folderID, folders, aclByFolder)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v，期望 %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.FolderID != tc.folderID || got.SourceID != tc.wantSource || got.OwnerID != tc.wantOwner || got.Explicit != tc.wantExp {
				t.Fatalf("解析结果 = %+v，期望 source=%d owner=%d explicit=%v",
					got, tc.wantSource, tc.wantOwner, tc.wantExp)
			}
			if len(got.Entries) != len(tc.wantACL) {
				t.Fatalf("ACL 条目 = %+v，期望 %+v", got.Entries, tc.wantACL)
			}
			for i := range tc.wantACL {
				if got.Entries[i] != tc.wantACL[i] {
					t.Fatalf("ACL 条目[%d] = %+v，期望 %+v", i, got.Entries[i], tc.wantACL[i])
				}
			}
		})
	}
}

// TestDocumentViewerPermitsACL 锁定四类主体的命中/不命中。
func TestDocumentViewerPermitsACL(t *testing.T) {
	viewer := docViewer(7, []int64{101, 102}, []int64{9}, false)
	cases := []struct {
		name string
		acl  []DocumentACLTarget
		want bool
	}{
		{name: "全员命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetAll, 0)}, want: true},
		{name: "部门子树命中（查看者所在部门）", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetDept, 102)}, want: true},
		{name: "部门祖先命中（被选部门是查看者祖先）", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetDept, 101)}, want: true},
		{name: "部门不命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetDept, 103)}, want: false},
		{name: "角色命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetRole, 9)}, want: true},
		{name: "角色不命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetRole, 8)}, want: false},
		{name: "用户命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetUser, 7)}, want: true},
		{name: "用户不命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetUser, 8)}, want: false},
		{name: "多条目任一命中", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetUser, 8), docACL(model.DocumentACLTargetRole, 9)}, want: true},
		{name: "空 ACL 不命中", acl: nil, want: false},
		{name: "未知主体类型不命中", acl: []DocumentACLTarget{docACL(9, 7)}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := viewer.PermitsACL(tc.acl); got != tc.want {
				t.Fatalf("PermitsACL(%+v) = %v，期望 %v", tc.acl, got, tc.want)
			}
		})
	}

	// 未登录（UserID<=0）fail-closed：全员 ACL 也不可见。
	anonymous := DocumentViewerOf(0, nil, nil, false)
	if anonymous.PermitsACL([]DocumentACLTarget{docACL(model.DocumentACLTargetAll, 0)}) {
		t.Fatal("未登录查看者不应命中任何 ACL")
	}
	if DocumentFolderVisible(anonymous, docFolder(1, 0, 0), EffectiveDocumentACL{}) {
		t.Fatal("未登录查看者不应看到目录")
	}
}

// TestDocumentFolderVisible 锁定可见性合成：owner/管理员旁路、无 ACL 顶层、
// 子覆盖父（不叠加父目录 ACL）。
func TestDocumentFolderVisible(t *testing.T) {
	aclAll := []DocumentACLTarget{docACL(model.DocumentACLTargetAll, 0)}
	aclDept102 := []DocumentACLTarget{docACL(model.DocumentACLTargetDept, 102)}

	dept102User := docViewer(7, []int64{100, 102}, []int64{9}, false)
	dept101User := docViewer(8, []int64{100, 101}, []int64{9}, false)
	owner := docViewer(10, []int64{103}, nil, false)
	admin := docViewer(1, nil, nil, true)

	folders := docFolderMap(
		docFolder(1, 0, 10), // A：全员
		docFolder(2, 1, 10), // B：部门 102（覆盖 A）
		docFolder(3, 1, 10), // C：无 ACL（继承 A）
		docFolder(4, 0, 10), // D：无 ACL 顶层
		docFolder(5, 4, 10), // E：用户 8（覆盖 D 的无 ACL）
	)
	aclByFolder := map[int64][]DocumentACLTarget{
		1: aclAll,
		2: aclDept102,
		5: {docACL(model.DocumentACLTargetUser, 8)},
	}

	cases := []struct {
		name     string
		viewer   DocumentViewer
		folderID int64
		want     bool
	}{
		{name: "全员 ACL 对任意登录用户可见", viewer: dept101User, folderID: 1, want: true},
		{name: "继承父目录全员 ACL", viewer: dept101User, folderID: 3, want: true},
		{name: "子目录部门 ACL 命中", viewer: dept102User, folderID: 2, want: true},
		{name: "子目录覆盖父目录：外部门不可见", viewer: dept101User, folderID: 2, want: false},
		{name: "无 ACL 顶层普通用户不可见", viewer: dept101User, folderID: 4, want: false},
		{name: "无 ACL 顶层 owner 可见", viewer: owner, folderID: 4, want: true},
		{name: "无 ACL 顶层管理员可见", viewer: admin, folderID: 4, want: true},
		{name: "无 ACL 顶层下的显式用户 ACL", viewer: dept101User, folderID: 5, want: true},
		{name: "无 ACL 顶层下的显式用户 ACL 对其他用户不可见", viewer: dept102User, folderID: 5, want: false},
		{name: "owner 旁路（ACL 不含 owner）", viewer: owner, folderID: 2, want: true},
		{name: "管理员旁路", viewer: admin, folderID: 2, want: true},
		{name: "未知目录不可见", viewer: admin, folderID: 99, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			folder, ok := folders[tc.folderID]
			if !ok {
				if tc.want {
					t.Fatalf("测试数据缺少目录 %d", tc.folderID)
				}
				return
			}
			effective, _ := ResolveEffectiveDocumentACL(tc.folderID, folders, aclByFolder)
			if got := DocumentFolderVisible(tc.viewer, folder, effective); got != tc.want {
				t.Fatalf("DocumentFolderVisible = %v，期望 %v（effective=%+v）", got, tc.want, effective)
			}
		})
	}
}

// TestDocumentFolderCanManage 锁定管理权：owner 或 oa:document:manage。
func TestDocumentFolderCanManage(t *testing.T) {
	folder := docFolder(1, 0, 10)
	cases := []struct {
		name   string
		viewer DocumentViewer
		want   bool
	}{
		{name: "owner 可管理", viewer: docViewer(10, nil, nil, false), want: true},
		{name: "管理员可管理", viewer: docViewer(99, nil, nil, true), want: true},
		{name: "非 owner 普通用户不可管理", viewer: docViewer(99, nil, nil, false), want: false},
		{name: "未登录不可管理", viewer: DocumentViewerOf(0, nil, nil, true), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DocumentFolderCanManage(folder, tc.viewer); got != tc.want {
				t.Fatalf("DocumentFolderCanManage = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestDocumentFileAccessGuards 锁定文件上传/删除的守卫语义（issue #59）：
// 上传与删除复用目录管理权（owner 或 oa:document:manage），列表/下载复用
// 目录读可见性；未登录/非 owner 普通用户一律不通过。
func TestDocumentFileAccessGuards(t *testing.T) {
	folder := docFolder(7, 0, 10)
	effective := EffectiveDocumentACL{
		FolderID: folder.ID,
		OwnerID:  folder.OwnerID,
		SourceID: folder.ID,
		Explicit: true,
		Entries:  []DocumentACLTarget{docACL(model.DocumentACLTargetUser, 42)},
	}
	cases := []struct {
		name        string
		viewer      DocumentViewer
		wantManage  bool
		wantVisible bool
	}{
		{name: "owner 可上传/删除且可读", viewer: docViewer(10, nil, nil, false), wantManage: true, wantVisible: true},
		{name: "管理员可上传/删除且可读", viewer: docViewer(99, nil, nil, true), wantManage: true, wantVisible: true},
		{name: "ACL 命中用户只读不可管理", viewer: docViewer(42, nil, nil, false), wantManage: false, wantVisible: true},
		{name: "其他用户不可读不可管理", viewer: docViewer(99, nil, nil, false), wantManage: false, wantVisible: false},
		{name: "未登录不可读不可管理", viewer: DocumentViewerOf(0, nil, nil, true), wantManage: false, wantVisible: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DocumentFolderCanManage(folder, tc.viewer); got != tc.wantManage {
				t.Fatalf("上传/删除管理权 = %v，期望 %v", got, tc.wantManage)
			}
			if got := DocumentFolderVisible(tc.viewer, folder, effective); got != tc.wantVisible {
				t.Fatalf("文件列表/下载读权限 = %v，期望 %v", got, tc.wantVisible)
			}
		})
	}
}

// TestValidateDocumentFileName 锁定文件展示名长度/空白校验（issue #59）。
func TestValidateDocumentFileName(t *testing.T) {
	if got, err := ValidateDocumentFileName("  季度报告.pdf  "); err != nil || got != "季度报告.pdf" {
		t.Fatalf("去空白失败: got=%q err=%v", got, err)
	}
	if _, err := ValidateDocumentFileName("   "); err == nil || !isBadRequest(err) {
		t.Fatalf("空文件名应 400，得到 %v", err)
	}
	long := make([]rune, MaxDocumentFileNameLen+1)
	for i := range long {
		long[i] = '文'
	}
	if _, err := ValidateDocumentFileName(string(long)); err == nil || !isBadRequest(err) {
		t.Fatalf("超长文件名应 400，得到 %v", err)
	}
}

// TestNormalizeDocumentACLTargets 锁定 ACL 入参归一化与校验。
func TestNormalizeDocumentACLTargets(t *testing.T) {
	got, err := NormalizeDocumentACLTargets([]DocumentACLTarget{
		docACL(model.DocumentACLTargetUser, 8),
		docACL(model.DocumentACLTargetAll, 0),
		docACL(model.DocumentACLTargetDept, 102),
		docACL(model.DocumentACLTargetDept, 101),
		docACL(model.DocumentACLTargetDept, 102),
	})
	if err != nil {
		t.Fatalf("归一化合法 ACL: %v", err)
	}
	want := []DocumentACLTarget{
		docACL(model.DocumentACLTargetAll, 0),
		docACL(model.DocumentACLTargetDept, 101),
		docACL(model.DocumentACLTargetDept, 102),
		docACL(model.DocumentACLTargetUser, 8),
	}
	if len(got) != len(want) {
		t.Fatalf("归一化结果 = %+v，期望 %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("归一化结果[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}

	badCases := []struct {
		name string
		acl  []DocumentACLTarget
	}{
		{name: "未知主体类型", acl: []DocumentACLTarget{docACL(9, 1)}},
		{name: "全员带非零目标", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetAll, 7)}},
		{name: "部门缺目标", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetDept, 0)}},
		{name: "角色负目标", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetRole, -1)}},
		{name: "用户缺目标", acl: []DocumentACLTarget{docACL(model.DocumentACLTargetUser, 0)}},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizeDocumentACLTargets(tc.acl); err == nil {
				t.Fatalf("期望 400，得到 nil")
			} else if !isBadRequest(err) {
				t.Fatalf("期望 400，得到 %v", err)
			}
		})
	}
}

// TestValidateDocumentFolderACL 锁定顶层目录必须显式 ACL。
func TestValidateDocumentFolderACL(t *testing.T) {
	entries := []DocumentACLTarget{docACL(model.DocumentACLTargetAll, 0)}
	if err := ValidateDocumentFolderACL(entries, true); err != nil {
		t.Fatalf("顶层带 ACL 应合法: %v", err)
	}
	if err := ValidateDocumentFolderACL(nil, false); err != nil {
		t.Fatalf("子目录空 ACL 表示继承，应合法: %v", err)
	}
	if err := ValidateDocumentFolderACL(nil, true); err == nil {
		t.Fatal("顶层空 ACL 应 400")
	} else if !isBadRequest(err) {
		t.Fatalf("期望 400，得到 %v", err)
	}
}

// TestValidateDocumentFolderName 锁定目录名长度/空白校验。
func TestValidateDocumentFolderName(t *testing.T) {
	if got, err := ValidateDocumentFolderName("  技术资料  "); err != nil || got != "技术资料" {
		t.Fatalf("去空白失败: got=%q err=%v", got, err)
	}
	if _, err := ValidateDocumentFolderName("   "); err == nil {
		t.Fatal("空目录名应 400")
	}
	long := make([]rune, MaxDocumentFolderNameLen+1)
	for i := range long {
		long[i] = '文'
	}
	if _, err := ValidateDocumentFolderName(string(long)); err == nil {
		t.Fatal("超长目录名应 400")
	}
}

// TestValidateDocumentFolderDelete 锁定非空目录删除返回 409。
func TestValidateDocumentFolderDelete(t *testing.T) {
	if err := ValidateDocumentFolderDelete(0, 0); err != nil {
		t.Fatalf("空目录应可删除: %v", err)
	}
	for _, tc := range []struct {
		name       string
		childCount int64
		fileCount  int64
	}{
		{name: "有子目录", childCount: 1},
		{name: "有文件", fileCount: 1},
		{name: "两者都有", childCount: 2, fileCount: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDocumentFolderDelete(tc.childCount, tc.fileCount)
			if err == nil {
				t.Fatal("非空目录删除应 409")
			}
			var xe *xerr.Error
			if !errors.As(err, &xe) || xe.Status != 409 {
				t.Fatalf("期望 409，得到 %v", err)
			}
		})
	}
}

// isBadRequest 报告错误是否为 400。
func isBadRequest(err error) bool {
	var xe *xerr.Error
	return errors.As(err, &xe) && xe.Status == 400
}
