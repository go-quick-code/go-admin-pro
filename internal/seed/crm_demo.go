// crm_demo.go 写入 CRM 演示数据（issue #21）：
//   - 演示组织：销售 / 销售主管两个角色，sales1 / sales2 / manager 三个账号，
//     角色绑定 CRM 全部菜单与按钮（含仪表盘、客户、联系人、商机、合同、回款、
//     公海、导入导出）；
//   - 演示业务：产品分类/产品、客户（含公海）、联系人、跟进（客户/商机/合同
//     三个维度）、商机（阶段 1–6）、合同（草稿/待审/已签/作废）、回款计划与
//     记录（待回款/部分回款/已回款/逾期），时间分布在近 12 个月；
//   - 编号按 crm/service 的 Format 函数生成，并同步写 crm_sequence，保证
//     后续通过 UI 新建不会与演示编号冲突。
//
// 幂等：crm_customer 已有任何行（含软删除）时整段跳过。演示行使用固定 ID 段
// （1001 起），运行期数据由自增 ID 发放，不会撞主键；角色/用户按 code/username
// FirstOrCreate，重复执行或升级库不会重复插入。
package seed

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	crmservice "github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	sysservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	demoTenantID  int64 = 1
	demoSalesDept int64 = 102 // 市场部：演示销售与主管所在部门
)

// demoOrgUsers 是演示组织解析后的用户 ID/部门快照。
type demoOrgUsers struct {
	admin   demoOrgUser
	sales1  demoOrgUser
	sales2  demoOrgUser
	manager demoOrgUser
}

type demoOrgUser struct {
	ID     int64
	DeptID int64
}

func (u demoOrgUser) creator() string {
	if u.ID <= 0 {
		return "0"
	}
	return strconv.FormatInt(u.ID, 10)
}

// init 把演示数据入口注册给 seed.Run（见 seed.go 的 seedCRMDemoFunc 注释）。
func init() { seedCRMDemoFunc = seedCRMDemo }

// seedCRMDemo 写入 CRM 演示数据。只有 crm_customer 为空时才执行。
func seedCRMDemo(db *gorm.DB) error {
	var customerCount int64
	if err := tenant.Skip(db).Unscoped().Model(&crmmodel.Customer{}).Count(&customerCount).Error; err != nil {
		return fmt.Errorf("统计 crm_customer: %w", err)
	}
	if customerCount > 0 {
		slog.Info("seed crm demo skipped", "reason", "crm_customer not empty", "customers", customerCount)
		return nil
	}

	err := tenant.Skip(db).Transaction(func(tx *gorm.DB) error {
		users, err := seedCRMDemoOrg(tx)
		if err != nil {
			return err
		}
		return seedCRMDemoBusiness(tx, users)
	})
	if err != nil {
		return err
	}
	slog.Info("seed crm demo created",
		"accounts", "sales1/sales2/manager (123456)",
		"customers", 8, "contracts", 6, "receivablePlans", 6)
	return nil
}

// seedCRMDemoOrg 创建（或复用）演示角色、用户、角色菜单与用户角色。
// 角色按 code、用户按 username 判重，重复执行不重复插入。
func seedCRMDemoOrg(tx *gorm.DB) (demoOrgUsers, error) {
	var users demoOrgUsers

	salesRole, err := ensureDemoRole(tx, "销售", "sales", 10, sysmodel.DataScopeSelf,
		"演示角色：仅本人数据，可维护自己的客户与销售流程")
	if err != nil {
		return users, err
	}
	managerRole, err := ensureDemoRole(tx, "销售主管", "sales_manager", 11, sysmodel.DataScopeDeptAndSub,
		"演示角色：本部门及以下数据，可查看市场部全部销售数据")
	if err != nil {
		return users, err
	}

	hash, err := sysservice.HashPassword("123456")
	if err != nil {
		return users, fmt.Errorf("生成演示账号密码: %w", err)
	}
	sales1, err := ensureDemoUser(tx, "sales1", "销售一", demoSalesDept, "13800000011", hash, "演示销售：市场部，仅本人数据")
	if err != nil {
		return users, err
	}
	sales2, err := ensureDemoUser(tx, "sales2", "销售二", demoSalesDept, "13800000012", hash, "演示销售：市场部，仅本人数据")
	if err != nil {
		return users, err
	}
	manager, err := ensureDemoUser(tx, "manager", "销售主管", demoSalesDept, "13800000013", hash, "演示销售主管：市场部，本部门及以下数据")
	if err != nil {
		return users, err
	}
	admin, err := findDemoUser(tx, "admin")
	if err != nil {
		return users, err
	}
	users = demoOrgUsers{admin: admin, sales1: sales1, sales2: sales2, manager: manager}

	menuIDs, err := crmDemoMenuIDs(tx)
	if err != nil {
		return users, err
	}
	for _, roleID := range []int64{salesRole.ID, managerRole.ID} {
		if err := bindDemoRoleMenus(tx, roleID, menuIDs); err != nil {
			return users, err
		}
	}
	for _, pair := range [][2]int64{
		{sales1.ID, salesRole.ID},
		{sales2.ID, salesRole.ID},
		{manager.ID, managerRole.ID},
	} {
		row := sysmodel.UserRole{TenantID: demoTenantID, UserID: pair[0], RoleID: pair[1]}
		if err := tenant.Skip(tx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return users, fmt.Errorf("绑定演示用户角色 user=%d role=%d: %w", pair[0], pair[1], err)
		}
	}
	return users, nil
}

// ensureDemoRole 按 code 查找角色，不存在时创建。
func ensureDemoRole(tx *gorm.DB, name, code string, sort int, scope int8, remark string) (*sysmodel.Role, error) {
	role := sysmodel.Role{
		TenantID: demoTenantID, Name: name, Code: code, Sort: sort,
		DataScope: scope, Status: sysmodel.StatusEnable, Type: 2, Remark: remark,
	}
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND code = ?", demoTenantID, code).
		FirstOrCreate(&role).Error
	if err != nil {
		return nil, fmt.Errorf("创建演示角色 %s: %w", code, err)
	}
	return &role, nil
}

// ensureDemoUser 按 username 查找用户，不存在时创建（已有账号不重置密码）。
func ensureDemoUser(tx *gorm.DB, username, nickname string, deptID int64, mobile, hash, remark string) (demoOrgUser, error) {
	user := sysmodel.User{
		TenantID: demoTenantID, Username: username, Password: hash, Nickname: nickname,
		DeptID: deptID, Email: username + "@example.com", Mobile: mobile,
		Status: sysmodel.StatusEnable, Remark: remark,
	}
	err := tenant.Skip(tx).
		Where("tenant_id = ? AND username = ?", demoTenantID, username).
		FirstOrCreate(&user).Error
	if err != nil {
		return demoOrgUser{}, fmt.Errorf("创建演示用户 %s: %w", username, err)
	}
	return demoOrgUser{ID: user.ID, DeptID: user.DeptID}, nil
}

// findDemoUser 读取基础种子中的用户（admin）。
func findDemoUser(tx *gorm.DB, username string) (demoOrgUser, error) {
	var user sysmodel.User
	if err := tenant.Skip(tx).
		Where("tenant_id = ? AND username = ?", demoTenantID, username).
		First(&user).Error; err != nil {
		return demoOrgUser{}, fmt.Errorf("读取基础种子用户 %s: %w", username, err)
	}
	return demoOrgUser{ID: user.ID, DeptID: user.DeptID}, nil
}

// crmDemoMenuIDs 返回 CRM 目录（400）与全部 CRM 权限菜单/按钮。
// 按权限码前缀查询而不是硬编码 ID 列表：新增 CRM 菜单时演示角色自动获得授权，
// 与 seedMenus 的权限码约定保持一致。
//
// 额外绑定 system:user:simple-list（PR #39 残余风险 C）：CRM 客户页的负责人
// 下拉调用用户简表，该接口已收口为需要权限码，演示销售角色必须显式持有，
// 否则演示账号无法选择负责人。
func crmDemoMenuIDs(tx *gorm.DB) ([]int64, error) {
	var ids []int64
	err := tenant.Skip(tx).Model(&sysmodel.Menu{}).
		Where("id = ? OR permission LIKE ? OR permission = ?", 400, "crm:%", "system:user:simple-list").
		Order("id ASC").Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查询 CRM 菜单: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("未找到 CRM 菜单（id=400、crm:* 或 system:user:simple-list）：请确认 seedMenus 已执行")
	}
	return ids, nil
}

// bindDemoRoleMenus 给角色补绑 CRM 菜单（复合主键 + ON CONFLICT DO NOTHING 幂等）。
func bindDemoRoleMenus(tx *gorm.DB, roleID int64, menuIDs []int64) error {
	rows := make([]sysmodel.RoleMenu, 0, len(menuIDs))
	for _, menuID := range menuIDs {
		rows = append(rows, sysmodel.RoleMenu{TenantID: demoTenantID, RoleID: roleID, MenuID: menuID})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := tenant.Skip(tx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
		return fmt.Errorf("绑定演示角色菜单 role=%d: %w", roleID, err)
	}
	return nil
}

// ---------- 演示业务数据 ----------

// demoCustomerSpec 是一条客户演示数据的声明（负责人用 username 解析）。
type demoCustomerSpec struct {
	ID            int64
	Name          string
	Industry      string
	Level         string
	Source        string
	Phone         string
	Website       string
	Address       string
	Status        int8
	Owner         string // 空串表示公海客户
	OriginalOwner string // 负责人历史中的首任（公海客户的释放来源）
	CreatedAt     time.Time
	LastFollow    *time.Time
	PoolEnter     *time.Time
	ReleaseReason string
	ReleasedBy    string // 释放操作人 username（公海客户必填）
}

// seedCRMDemoBusiness 在一个事务里写入产品、客户、联系人、跟进、商机、合同、回款与序列。
func seedCRMDemoBusiness(tx *gorm.DB, users demoOrgUsers) error {
	now := time.Now()
	seq := newDemoSequencer(tx)

	if err := seedDemoProducts(tx, now); err != nil {
		return err
	}
	if err := seedDemoCustomers(tx, now, users, seq); err != nil {
		return err
	}
	if err := seedDemoContacts(tx, now); err != nil {
		return err
	}
	if err := seedDemoFollowUps(tx, now, users); err != nil {
		return err
	}
	if err := seedDemoOpportunities(tx, now, users, seq); err != nil {
		return err
	}
	if err := seedDemoContracts(tx, now, users, seq); err != nil {
		return err
	}
	if err := seedDemoReceivables(tx, now, seq); err != nil {
		return err
	}
	if err := seq.flush(); err != nil {
		return err
	}
	return nil
}

// seedDemoProducts 写入 2 个产品分类与 4 个产品（价格覆盖高/中/低）。
func seedDemoProducts(tx *gorm.DB, now time.Time) error {
	created := demoMonthsAgo(now, 9, 6)
	categories := []crmmodel.ProductCategory{
		{BaseEntity: demoBase(1001, created, "1"), TenantID: demoTenantID, ParentID: 0, Name: "软件产品", Sort: 1, Status: crmmodel.CategoryStatusEnable},
		{BaseEntity: demoBase(1002, created, "1"), TenantID: demoTenantID, ParentID: 0, Name: "实施服务", Sort: 2, Status: crmmodel.CategoryStatusEnable},
	}
	if err := tenant.Skip(tx).Create(&categories).Error; err != nil {
		return fmt.Errorf("写入演示产品分类: %w", err)
	}

	products := []crmmodel.Product{
		{
			BaseEntity: demoBase(1001, created, "1"), TenantID: demoTenantID, CategoryId: 1001,
			Name: "CRM 企业版", Code: "DEMO-CRM-001", Unit: "套",
			Price: demoMoney(12800000), Status: crmmodel.ProductStatusOnSale,
			Description: "面向中大型企业的客户关系管理套件",
		},
		{
			BaseEntity: demoBase(1002, created, "1"), TenantID: demoTenantID, CategoryId: 1001,
			Name: "CRM 专业版", Code: "DEMO-CRM-002", Unit: "套",
			Price: demoMoney(6800000), Status: crmmodel.ProductStatusOnSale,
			Description: "面向成长型团队的客户关系管理套件",
		},
		{
			BaseEntity: demoBase(1003, created, "1"), TenantID: demoTenantID, CategoryId: 1002,
			Name: "MES 实施服务", Code: "DEMO-MES-003", Unit: "套",
			Price: demoMoney(25000000), Status: crmmodel.ProductStatusOnSale,
			Description: "制造执行系统实施与交付",
		},
		{
			BaseEntity: demoBase(1004, created, "1"), TenantID: demoTenantID, CategoryId: 1002,
			Name: "数字化咨询服务", Code: "DEMO-CONSULT-004", Unit: "人天",
			Price: demoMoney(4500000), Status: crmmodel.ProductStatusOnSale,
			Description: "数字化规划与流程咨询服务",
		},
	}
	if err := tenant.Skip(tx).Create(&products).Error; err != nil {
		return fmt.Errorf("写入演示产品: %w", err)
	}
	return nil
}

// seedDemoCustomers 写入 8 个客户（6 个有负责人 + 2 个公海），并写负责人历史。
func seedDemoCustomers(tx *gorm.DB, now time.Time, users demoOrgUsers, seq *demoSequencer) error {
	byName := map[string]demoOrgUser{
		"admin": users.admin, "sales1": users.sales1, "sales2": users.sales2, "manager": users.manager,
	}
	lastFollow := func(days int) *time.Time {
		t := demoDaysAgo(now, days)
		return &t
	}
	specs := []demoCustomerSpec{
		{
			ID: 1001, Name: "云启科技有限公司", Industry: "软件与信息服务", Level: "A", Source: "展会",
			Phone: "010-88880001", Website: "https://www.yunqi.example.com", Address: "北京市海淀区中关村大街 1 号",
			Status: crmmodel.CustomerStatusActive, Owner: "admin", OriginalOwner: "admin",
			CreatedAt: demoMonthsAgo(now, 8, 12), LastFollow: lastFollow(5),
		},
		{
			ID: 1002, Name: "恒达智能制造股份有限公司", Industry: "装备制造", Level: "A", Source: "转介绍",
			Phone: "021-66660002", Website: "https://www.hengda.example.com", Address: "上海市浦东新区张江路 88 号",
			Status: crmmodel.CustomerStatusActive, Owner: "admin", OriginalOwner: "admin",
			CreatedAt: demoMonthsAgo(now, 7, 8), LastFollow: lastFollow(8),
		},
		{
			ID: 1003, Name: "星海文化传媒有限公司", Industry: "广告传媒", Level: "B", Source: "官网",
			Phone: "020-88880003", Website: "https://www.xinghai.example.com", Address: "广州市天河区体育西路 100 号",
			Status: crmmodel.CustomerStatusPotential, Owner: "sales1", OriginalOwner: "sales1",
			CreatedAt: demoMonthsAgo(now, 5, 20), LastFollow: lastFollow(3),
		},
		{
			ID: 1004, Name: "北辰物流集团有限公司", Industry: "物流运输", Level: "A", Source: "电话",
			Phone: "028-88880004", Website: "https://www.beichen.example.com", Address: "成都市高新区天府大道 666 号",
			Status: crmmodel.CustomerStatusActive, Owner: "sales1", OriginalOwner: "sales1",
			CreatedAt: demoMonthsAgo(now, 4, 15), LastFollow: lastFollow(40),
		},
		{
			ID: 1005, Name: "蓝湾生物科技有限公司", Industry: "生物医药", Level: "B", Source: "展会",
			Phone: "0571-88880005", Website: "https://www.lanwan.example.com", Address: "杭州市滨江区江南大道 500 号",
			Status: crmmodel.CustomerStatusActive, Owner: "sales2", OriginalOwner: "sales2",
			CreatedAt: demoMonthsAgo(now, 3, 10), LastFollow: lastFollow(10),
		},
		{
			ID: 1006, Name: "红叶连锁零售有限公司", Industry: "零售连锁", Level: "C", Source: "广告",
			Phone: "025-88880006", Website: "https://www.hongye.example.com", Address: "南京市建邺区江东中路 200 号",
			Status: crmmodel.CustomerStatusLost, Owner: "manager", OriginalOwner: "manager",
			CreatedAt: demoMonthsAgo(now, 2, 18), LastFollow: lastFollow(70),
		},
		{
			ID: 1007, Name: "南风新能源有限公司", Industry: "新能源", Level: "B", Source: "官网",
			Phone: "029-88880007", Website: "https://www.nanfeng.example.com", Address: "西安市高新区科技路 30 号",
			Status: crmmodel.CustomerStatusPotential, Owner: "", OriginalOwner: "sales1",
			CreatedAt: demoMonthsAgo(now, 6, 5), LastFollow: lastFollow(60),
			PoolEnter: demoPtrTime(demoDaysAgo(now, 15)), ReleaseReason: "超过 60 天未跟进，手动释放公海",
			ReleasedBy: "admin",
		},
		{
			ID: 1008, Name: "启明教育科技有限公司", Industry: "教育培训", Level: "C", Source: "转介绍",
			Phone: "027-88880008", Website: "https://www.qiming.example.com", Address: "武汉市洪山区珞喻路 129 号",
			Status: crmmodel.CustomerStatusPotential, Owner: "", OriginalOwner: "sales2",
			CreatedAt: demoMonthsAgo(now, 4, 22),
			PoolEnter: demoPtrTime(demoDaysAgo(now, 5)), ReleaseReason: "销售离职，客户释放公海",
			ReleasedBy: "admin",
		},
	}

	customers := make([]crmmodel.Customer, 0, len(specs))
	poolIDs := make([]int64, 0, 2)
	for _, spec := range specs {
		owner := byName[spec.Owner]
		deptID := owner.DeptID
		if spec.Owner == "" {
			deptID = 0
			poolIDs = append(poolIDs, spec.ID)
		}
		customer := crmmodel.Customer{
			BaseEntity:     demoBase(spec.ID, spec.CreatedAt, byName[spec.OriginalOwner].creator()),
			TenantID:       demoTenantID,
			Name:           spec.Name,
			Industry:       spec.Industry,
			Level:          spec.Level,
			Source:         spec.Source,
			Phone:          spec.Phone,
			Website:        spec.Website,
			Address:        spec.Address,
			Status:         spec.Status,
			OwnerUserID:    owner.ID,
			OwnerDeptID:    deptID,
			LastFollowTime: spec.LastFollow,
			PoolEnterTime:  spec.PoolEnter,
			ReleaseReason:  spec.ReleaseReason,
		}
		no, err := seq.nextCustomer(spec.CreatedAt)
		if err != nil {
			return err
		}
		customer.CustomerNo = no
		customers = append(customers, customer)
	}
	if err := tenant.Skip(tx).Create(&customers).Error; err != nil {
		return fmt.Errorf("写入演示客户: %w", err)
	}
	// 公海客户没有负责人：owner 列必须落 NULL（结构体零值会被写成 0，
	// 而公海谓词/认领条件都判断 IS NULL）。
	if len(poolIDs) > 0 {
		if err := tenant.Skip(tx).Model(&crmmodel.Customer{}).
			Where("id IN ?", poolIDs).
			Updates(map[string]any{"owner_user_id": nil, "owner_dept_id": nil}).Error; err != nil {
			return fmt.Errorf("写入公海客户负责人 NULL: %w", err)
		}
	}

	histories := make([]crmmodel.CustomerOwnerHistory, 0, len(specs)+2)
	historyID := int64(1001)
	for _, spec := range specs {
		creator := byName[spec.OriginalOwner]
		histories = append(histories, crmmodel.CustomerOwnerHistory{
			ID: historyID, CreatedAt: spec.CreatedAt, TenantID: demoTenantID,
			CustomerID: spec.ID, Action: crmmodel.OwnerHistoryCreate,
			ToOwnerUserID: creator.ID, ToOwnerDeptID: creator.DeptID,
			OperatorUserID: creator.ID,
		})
		historyID++
		if spec.Owner == "" {
			releaser := byName[spec.ReleasedBy]
			histories = append(histories, crmmodel.CustomerOwnerHistory{
				ID: historyID, CreatedAt: *spec.PoolEnter, TenantID: demoTenantID,
				CustomerID: spec.ID, Action: crmmodel.OwnerHistoryRelease,
				FromOwnerUserID: creator.ID, FromOwnerDeptID: creator.DeptID,
				OperatorUserID: releaser.ID,
			})
			historyID++
		}
	}
	if err := tenant.Skip(tx).Create(&histories).Error; err != nil {
		return fmt.Errorf("写入演示负责人历史: %w", err)
	}
	return nil
}

// seedDemoContacts 写入 12 个联系人（每客户 1–2 个）。
func seedDemoContacts(tx *gorm.DB, now time.Time) error {
	type spec struct {
		id                     int64
		customerID             int64
		name, position, mobile string
		email, decisionRole    string
		gender                 int8
		createdAt              time.Time
	}
	specs := []spec{
		{1001, 1001, "张伟", "信息总监", "13800001001", "zhangwei@yunqi.example.com", "决策人", 1, demoMonthsAgo(now, 8, 13)},
		{1002, 1001, "李娜", "采购经理", "13800001002", "lina@yunqi.example.com", "影响者", 2, demoMonthsAgo(now, 8, 14)},
		{1003, 1002, "王强", "制造总监", "13800001003", "wangqiang@hengda.example.com", "决策人", 1, demoMonthsAgo(now, 7, 9)},
		{1004, 1003, "陈晨", "品牌总监", "13800001004", "chenchen@xinghai.example.com", "决策人", 2, demoMonthsAgo(now, 5, 21)},
		{1005, 1003, "刘洋", "市场专员", "13800001005", "liuyang@xinghai.example.com", "使用者", 1, demoMonthsAgo(now, 5, 22)},
		{1006, 1004, "赵敏", "运营总监", "13800001006", "zhaomin@beichen.example.com", "决策人", 2, demoMonthsAgo(now, 4, 16)},
		{1007, 1005, "孙杰", "实验室主任", "13800001007", "sunjie@lanwan.example.com", "决策人", 1, demoMonthsAgo(now, 3, 11)},
		{1008, 1005, "周婷", "采购专员", "13800001008", "zhouting@lanwan.example.com", "影响者", 2, demoMonthsAgo(now, 3, 12)},
		{1009, 1006, "吴刚", "信息经理", "13800001009", "wugang@hongye.example.com", "使用者", 1, demoMonthsAgo(now, 2, 19)},
		{1010, 1007, "郑爽", "能源事业部总监", "13800001010", "zhengshuang@nanfeng.example.com", "决策人", 2, demoMonthsAgo(now, 6, 6)},
		{1011, 1007, "冯磊", "项目经理", "13800001011", "fenglei@nanfeng.example.com", "影响者", 1, demoMonthsAgo(now, 6, 7)},
		{1012, 1008, "何静", "校长助理", "13800001012", "hejing@qiming.example.com", "决策人", 2, demoMonthsAgo(now, 4, 23)},
	}
	contacts := make([]crmmodel.Contact, 0, len(specs))
	for _, s := range specs {
		contacts = append(contacts, crmmodel.Contact{
			BaseEntity: demoBase(s.id, s.createdAt, "1"), TenantID: demoTenantID,
			CustomerID: s.customerID, Name: s.name, Position: s.position,
			Mobile: s.mobile, Email: s.email, Gender: s.gender,
			DecisionRole: s.decisionRole,
		})
	}
	if err := tenant.Skip(tx).Create(&contacts).Error; err != nil {
		return fmt.Errorf("写入演示联系人: %w", err)
	}
	return nil
}

// seedDemoFollowUps 写入 9 条跟进：6 条客户维度 + 商机维度 + 合同维度 + 1 条客户补充。
func seedDemoFollowUps(tx *gorm.DB, now time.Time, users demoOrgUsers) error {
	type spec struct {
		id            int64
		customerID    int64
		opportunityID int64
		contractID    int64
		followType    int8
		content       string
		createdAt     time.Time
		nextTime      *time.Time
		creator       demoOrgUser
	}
	specs := []spec{
		{1001, 1001, 0, 0, crmmodel.FollowUpTypePhone, "电话回访 CRM 使用情况，客户满意，准备启动二期。", demoDaysAgo(now, 5), demoPtrTime(demoDaysFromNow(now, 3)), users.admin},
		{1002, 1002, 0, 0, crmmodel.FollowUpTypeVisit, "现场走访 MES 产线，确认扩容需求。", demoDaysAgo(now, 20), demoPtrTime(demoDaysFromNow(now, 7)), users.admin},
		{1003, 1003, 0, 0, crmmodel.FollowUpTypeWeChat, "微信发送品牌升级案例集。", demoDaysAgo(now, 3), demoPtrTime(demoDaysFromNow(now, 2)), users.sales1},
		{1004, 1004, 0, 0, crmmodel.FollowUpTypeEmail, "邮件发送运输管理系统报价与实施计划。", demoDaysAgo(now, 40), demoPtrTime(demoDaysFromNow(now, 10)), users.sales1},
		{1005, 1005, 0, 0, crmmodel.FollowUpTypePhone, "电话确认实验室数字化项目验收节点。", demoDaysAgo(now, 10), demoPtrTime(demoDaysFromNow(now, 5)), users.sales2},
		{1006, 1006, 0, 0, crmmodel.FollowUpTypeOther, "客户选择竞品会员系统，本期流失。", demoDaysAgo(now, 70), nil, users.manager},
		{1007, 1007, 0, 0, crmmodel.FollowUpTypePhone, "首次电话接触，客户暂无明确预算。", demoDaysAgo(now, 60), nil, users.sales1},
		{1008, 0, 1005, 0, crmmodel.FollowUpTypeVisit, "MES 项目复盘会，客户确认二期扩容意向。", demoDaysAgo(now, 8), demoPtrTime(demoDaysFromNow(now, 1)), users.admin},
		{1009, 0, 0, 1002, crmmodel.FollowUpTypePhone, "合同履约沟通，CRM 平台运行稳定。", demoDaysAgo(now, 12), demoPtrTime(demoDaysFromNow(now, 4)), users.admin},
	}
	rows := make([]crmmodel.FollowUp, 0, len(specs))
	for _, s := range specs {
		rows = append(rows, crmmodel.FollowUp{
			BaseEntity: demoBase(s.id, s.createdAt, s.creator.creator()), TenantID: demoTenantID,
			CustomerID: s.customerID, OpportunityID: s.opportunityID, ContractID: s.contractID,
			Type: s.followType, Content: s.content, NextTime: s.nextTime,
		})
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示跟进: %w", err)
	}
	return nil
}

// seedDemoOpportunities 写入 6 个商机（阶段 1–6 各一）与 5 条行项目。
func seedDemoOpportunities(tx *gorm.DB, now time.Time, users demoOrgUsers, seq *demoSequencer) error {
	type spec struct {
		id             int64
		customerID     int64
		name           string
		stage          int8
		expectedAmount int64 // 分
		expectedClose  *time.Time
		owner          demoOrgUser
		createdAt      time.Time
		productID      int64
		quantity       int64
		price          int64
		remark         string
	}
	specs := []spec{
		{1001, 1001, "云启科技 CRM 二期", crmmodel.OpportunityStageInitial, 12800000, demoPtrTime(demoDaysFromNow(now, 45)), users.admin, demoMonthsAgo(now, 2, 3), 1001, 1, 12800000, "客户已提出移动端审批需求"},
		{1002, 1003, "星海传媒品牌升级方案", crmmodel.OpportunityStageDemand, 6800000, demoPtrTime(demoDaysFromNow(now, 30)), users.sales1, demoMonthsAgo(now, 2, 12), 1002, 1, 6800000, "等待客户内部立项"},
		{1003, 1004, "北辰物流运输管理系统", crmmodel.OpportunityStageQuote, 25000000, demoPtrTime(demoDaysFromNow(now, 60)), users.sales1, demoMonthsAgo(now, 1, 6), 1003, 1, 25000000, "报价已提交，等待技术评审"},
		{1004, 1005, "蓝湾生物实验室数字化", crmmodel.OpportunityStageNegotiate, 4500000, demoPtrTime(demoDaysFromNow(now, 20)), users.sales2, demoMonthsAgo(now, 1, 18), 1004, 1, 4500000, "商务谈判中"},
		{1005, 1002, "恒达制造 MES 项目", crmmodel.OpportunityStageWon, 12800000, demoPtrTime(demoMonthsAgo(now, 10, 1)), users.admin, demoMonthsAgo(now, 11, 20), 1001, 1, 12800000, "已赢单并签订合同"},
		{1006, 1006, "红叶零售会员系统", crmmodel.OpportunityStageLost, 8800000, demoPtrTime(demoMonthsAgo(now, 2, 1)), users.manager, demoMonthsAgo(now, 3, 10), 0, 0, 0, "客户选择竞品"},
	}
	rows := make([]crmmodel.Opportunity, 0, len(specs))
	opportunityProducts := make([]crmmodel.OpportunityProduct, 0, len(specs))
	for _, s := range specs {
		no, err := seq.nextOpportunity(s.createdAt)
		if err != nil {
			return err
		}
		rows = append(rows, crmmodel.Opportunity{
			BaseEntity: demoBase(s.id, s.createdAt, s.owner.creator()), TenantID: demoTenantID,
			OpportunityNo: no, CustomerID: s.customerID, Name: s.name, Stage: s.stage,
			ExpectedAmount: demoMoney(s.expectedAmount), ExpectedCloseDate: s.expectedClose,
			OwnerUserID: s.owner.ID, OwnerDeptID: s.owner.DeptID, Remark: s.remark,
		})
		if s.productID > 0 {
			opportunityProducts = append(opportunityProducts, crmmodel.OpportunityProduct{
				OpportunityID: s.id, ProductID: s.productID, TenantID: demoTenantID,
				Quantity: s.quantity, Price: demoMoney(s.price),
				Amount: demoMoney(s.quantity * s.price),
			})
		}
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示商机: %w", err)
	}
	if err := tenant.Skip(tx).Create(&opportunityProducts).Error; err != nil {
		return fmt.Errorf("写入演示商机行项目: %w", err)
	}
	return nil
}

// seedDemoContracts 写入 6 个合同：3 已签（带行项目）、1 待审、1 草稿、1 作废。
func seedDemoContracts(tx *gorm.DB, now time.Time, users demoOrgUsers, seq *demoSequencer) error {
	type spec struct {
		id            int64
		customerID    int64
		opportunityID int64
		name          string
		amount        int64 // 分
		signDate      *time.Time
		startDate     *time.Time
		endDate       *time.Time
		status        int8
		owner         demoOrgUser
		createdAt     time.Time
		productID     int64
		quantity      int64
		price         int64
		remark        string
	}
	signed10 := demoMonthsAgo(now, 10, 15)
	signed5 := demoMonthsAgo(now, 5, 10)
	signed1 := demoMonthsAgo(now, 1, 8)
	voidSign := demoMonthsAgo(now, 2, 5)
	specs := []spec{
		{
			1001, 1002, 1005, "恒达制造 MES 项目合同", 12800000,
			&signed10, &signed10, demoPtrTime(signed10.AddDate(1, 0, 0)), crmmodel.ContractStatusSigned,
			users.admin, signed10.AddDate(0, 0, -3), 1001, 1, 12800000, "来源商机：恒达制造 MES 项目",
		},
		{
			1002, 1001, 0, "云启科技 CRM 平台采购合同", 12800000,
			&signed5, &signed5, demoPtrTime(signed5.AddDate(1, 0, 0)), crmmodel.ContractStatusSigned,
			users.admin, signed5.AddDate(0, 0, -5), 1001, 1, 12800000, "老客户续约",
		},
		{
			1003, 1005, 0, "蓝湾生物实验室数字化服务合同", 4500000,
			&signed1, &signed1, demoPtrTime(signed1.AddDate(0, 6, 0)), crmmodel.ContractStatusSigned,
			users.sales2, signed1.AddDate(0, 0, -2), 1004, 1, 4500000, "咨询服务 1 人天 × 1",
		},
		{
			1004, 1003, 1002, "星海传媒品牌升级框架合同", 6800000,
			nil, nil, nil, crmmodel.ContractStatusPending,
			users.sales1, demoDaysAgo(now, 10), 1002, 1, 6800000, "待客户确认后审批",
		},
		{
			1005, 1004, 1003, "北辰物流运输管理系统合同", 25000000,
			nil, nil, nil, crmmodel.ContractStatusDraft,
			users.sales1, demoDaysAgo(now, 5), 1003, 1, 25000000, "报价谈判中，合同草稿",
		},
		{
			1006, 1006, 0, "红叶零售会员系统合同（已作废）", 9000000,
			&voidSign, &voidSign, demoPtrTime(voidSign.AddDate(1, 0, 0)), crmmodel.ContractStatusVoid,
			users.manager, voidSign.AddDate(0, 0, -6), 1004, 2, 4500000, "客户改选竞品，合同作废",
		},
	}
	rows := make([]crmmodel.Contract, 0, len(specs))
	lineItems := make([]crmmodel.ContractProduct, 0, len(specs))
	for _, s := range specs {
		no, err := seq.nextContract(s.createdAt)
		if err != nil {
			return err
		}
		rows = append(rows, crmmodel.Contract{
			BaseEntity: demoBase(s.id, s.createdAt, s.owner.creator()), TenantID: demoTenantID,
			ContractNo: no, CustomerID: s.customerID, OpportunityID: s.opportunityID,
			Name: s.name, Amount: demoMoney(s.amount), SignDate: s.signDate,
			StartDate: s.startDate, EndDate: s.endDate, Status: s.status,
			OwnerUserID: s.owner.ID, OwnerDeptID: s.owner.DeptID, Remark: s.remark,
		})
		lineItems = append(lineItems, crmmodel.ContractProduct{
			ContractID: s.id, ProductID: s.productID, TenantID: demoTenantID,
			Quantity: s.quantity, Price: demoMoney(s.price),
			Amount: demoMoney(s.quantity * s.price),
		})
	}
	if err := tenant.Skip(tx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入演示合同: %w", err)
	}
	if err := tenant.Skip(tx).Create(&lineItems).Error; err != nil {
		return fmt.Errorf("写入演示合同行项目: %w", err)
	}
	return nil
}

// seedDemoReceivables 写入 6 期回款计划与 5 条回款记录：
// 已回款 3 期、待回款 1 期、部分回款 1 期、逾期 1 期；到账日期分布在
// 近 12 个月的 4 个以上月份，支撑仪表盘趋势。
func seedDemoReceivables(tx *gorm.DB, now time.Time, seq *demoSequencer) error {
	type planSpec struct {
		id         int64
		contractID int64
		periodNo   int
		amount     int64 // 分
		dueDate    time.Time
		remark     string
		createdAt  time.Time
	}
	plans := []planSpec{
		{1001, 1001, 1, 4000000, demoMonthsAgo(now, 9, 15), "首期款 31.25%", demoMonthsAgo(now, 10, 18)},
		{1002, 1001, 2, 4000000, demoMonthsAgo(now, 6, 15), "二期款 31.25%", demoMonthsAgo(now, 10, 18)},
		{1003, 1001, 3, 4800000, demoDaysFromNow(now, 30), "尾款 37.5%", demoMonthsAgo(now, 10, 18)},
		{1004, 1002, 1, 12800000, demoMonthsAgo(now, 4, 10), "一次性付款", demoMonthsAgo(now, 5, 15)},
		{1005, 1003, 1, 2250000, demoDaysAgo(now, 20), "首期款 50%", demoMonthsAgo(now, 1, 10)},
		{1006, 1003, 2, 2250000, demoDaysFromNow(now, 40), "验收款 50%", demoMonthsAgo(now, 1, 10)},
	}
	planRows := make([]crmmodel.ReceivablePlan, 0, len(plans))
	for _, plan := range plans {
		planRows = append(planRows, crmmodel.ReceivablePlan{
			BaseEntity: demoBase(plan.id, plan.createdAt, "1"), TenantID: demoTenantID,
			ContractID: plan.contractID, PeriodNo: plan.periodNo,
			Amount: demoMoney(plan.amount), DueDate: plan.dueDate, Remark: plan.remark,
		})
	}
	if err := tenant.Skip(tx).Create(&planRows).Error; err != nil {
		return fmt.Errorf("写入演示回款计划: %w", err)
	}

	type recordSpec struct {
		id          int64
		planID      int64
		contractID  int64
		amount      int64 // 分
		receiptDate time.Time
		method      string
		remark      string
	}
	records := []recordSpec{
		{1001, 1001, 1001, 4000000, demoMonthsAgo(now, 9, 20), "银行转账", "首期款到账"},
		{1002, 1002, 1001, 4000000, demoMonthsAgo(now, 6, 18), "银行转账", "二期款到账"},
		{1003, 1004, 1002, 12800000, demoMonthsAgo(now, 4, 12), "银行转账", "一次性付款到账"},
		{1004, 1005, 1003, 800000, demoDaysAgo(now, 10), "银行转账", "首期部分到账"},
		{1005, 1006, 1003, 1000000, demoDaysAgo(now, 5), "银行转账", "验收款部分到账"},
	}
	recordRows := make([]crmmodel.ReceivableRecord, 0, len(records))
	for _, record := range records {
		no, err := seq.nextReceivableRecord(record.receiptDate)
		if err != nil {
			return err
		}
		recordRows = append(recordRows, crmmodel.ReceivableRecord{
			BaseEntity: demoBase(record.id, record.receiptDate, "1"), TenantID: demoTenantID,
			PlanID: record.planID, ContractID: record.contractID, RecordNo: no,
			Amount: demoMoney(record.amount), ReceiptDate: record.receiptDate,
			Method: record.method, Remark: record.remark,
		})
	}
	if err := tenant.Skip(tx).Create(&recordRows).Error; err != nil {
		return fmt.Errorf("写入演示回款记录: %w", err)
	}
	return nil
}

// ---------- 编号序列 ----------

// demoSequencer 在事务内按 (biz_type, date_key) 分配演示编号：
// 起点取 crm_sequence 的现有 last_value + 1，避免与升级库/软删除行撞号；
// 结束时把最终值回写（GREATEST 保证只增不减）。
type demoSequencer struct {
	db       *gorm.DB
	tenantID int64
	last     map[string]int64
}

func newDemoSequencer(db *gorm.DB) *demoSequencer {
	return &demoSequencer{
		db:       db,
		tenantID: demoTenantID,
		last:     make(map[string]int64),
	}
}

func (s *demoSequencer) nextCustomer(at time.Time) (string, error) {
	seq, err := s.next(crmservice.BizTypeCustomer, at)
	if err != nil {
		return "", err
	}
	return crmservice.FormatCustomerNo(crmservice.DateKey(at), seq), nil
}

func (s *demoSequencer) nextOpportunity(at time.Time) (string, error) {
	seq, err := s.next(crmservice.BizTypeOpportunity, at)
	if err != nil {
		return "", err
	}
	return crmservice.FormatOpportunityNo(crmservice.DateKey(at), seq), nil
}

func (s *demoSequencer) nextContract(at time.Time) (string, error) {
	seq, err := s.next(crmservice.BizTypeContract, at)
	if err != nil {
		return "", err
	}
	return crmservice.FormatContractNo(crmservice.DateKey(at), seq), nil
}

func (s *demoSequencer) nextReceivableRecord(at time.Time) (string, error) {
	seq, err := s.next(crmservice.BizTypeReceivableRecord, at)
	if err != nil {
		return "", err
	}
	return crmservice.FormatReceivableRecordNo(crmservice.DateKey(at), seq), nil
}

func (s *demoSequencer) next(bizType string, at time.Time) (int64, error) {
	dateKey := crmservice.DateKey(at)
	key := bizType + "|" + dateKey
	if _, ok := s.last[key]; !ok {
		var current int64
		if err := tenant.Skip(s.db).Raw(
			"SELECT COALESCE(MAX(`last_value`), 0) FROM `crm_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?",
			s.tenantID, bizType, dateKey,
		).Scan(&current).Error; err != nil {
			return 0, fmt.Errorf("读取演示编号序列 %s/%s: %w", bizType, dateKey, err)
		}
		s.last[key] = current
	}
	s.last[key]++
	return s.last[key], nil
}

func (s *demoSequencer) flush() error {
	for key, value := range s.last {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			return fmt.Errorf("非法的演示序列键 %q", key)
		}
		if err := tenant.Skip(s.db).Exec(
			"INSERT INTO `crm_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (?, ?, ?, ?) "+
				"ON DUPLICATE KEY UPDATE `last_value` = GREATEST(`last_value`, ?)",
			s.tenantID, parts[0], parts[1], value, value,
		).Error; err != nil {
			return fmt.Errorf("写回演示编号序列 %s: %w", key, err)
		}
	}
	return nil
}

// ---------- 小工具 ----------

func demoBase(id int64, createdAt time.Time, creator string) basemodel.BaseEntity {
	if creator == "" {
		creator = "0"
	}
	return basemodel.BaseEntity{
		ID:        id,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Creator:   creator,
		Updater:   creator,
	}
}

func demoMoney(cents int64) money.Money {
	return money.FromCents(cents)
}

func demoPtrTime(t time.Time) *time.Time {
	return &t
}

// demoMonthsAgo 返回 n 个月前当月第 day 天的 10:00（业务时区按进程本地时区，
// 与 seed 的其余时间字段一致；跨月天数由 time.Date 归一化）。
func demoMonthsAgo(now time.Time, months, day int) time.Time {
	first := time.Date(now.Year(), now.Month(), 1, 10, 0, 0, 0, now.Location()).AddDate(0, -months, 0)
	return time.Date(first.Year(), first.Month(), day, 10, 0, 0, 0, first.Location())
}

func demoDaysAgo(now time.Time, days int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location()).
		AddDate(0, 0, -days)
}

func demoDaysFromNow(now time.Time, days int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location()).
		AddDate(0, 0, days)
}
