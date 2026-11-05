package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/notify/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// Input 是一条待投递通知的输入（生产者视角）。一次投递按接收人拆成多条
// Input 批量传入，每条物化一行（spec §4.13）。
type Input struct {
	UserID      int64  // 接收人用户 ID
	Type        string // 类型枚举：model.TypeBpmTodo 等（spec §4.13）
	Title       string // 标题
	Content     string // 内容摘要
	BizType     string // 业务类型（如 bpm_task/oa_announcement），与 BizID 定位业务
	BizID       int64  // 业务 ID
	Route       string // 前端跳转路由 path（空表示仅站内展示）
	RouteParams string // 跳转参数 JSON 对象字符串（如 {"taskId":1}）
}

// CreateInTx 在调用方事务 tx 内批量写入通知——本模块对外的主 seam：
// bpm/公告/会议室等生产者把已有的事务句柄传入，通知与源操作同生共死
// （spec §4.13：同一事务，写失败则源操作回滚）。
//
// 约束：
//   - tx 必须非 nil。通知不允许脱离调用方事务隐式自动提交——需要独立事务时
//     用 Create；误传 nil 视为装配错误直接报错。
//   - 空批次是合法 no-op（候选接收人为空的生产者流不必先判空）。
//   - 任一条 Input 校验失败则整批不写，返回错误交调用方回滚。
func (s *Service) CreateInTx(ctx context.Context, tx *gorm.DB, inputs []Input) error {
	if tx == nil {
		return xerr.Internal(errors.New("notify: CreateInTx 需要调用方事务句柄，独立写入请用 Create"))
	}
	rows, err := buildRows(inputs)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return s.repo.CreateBatch(ctx, tx, rows)
}

// Create 无既有事务的生产者入口：在本模块连接上自开事务批量写入，
// 事务语义与 CreateInTx 一致（校验失败整批不写）。
func (s *Service) Create(ctx context.Context, inputs []Input) error {
	return s.repo.Transaction(ctx, func(tx *gorm.DB) error {
		return s.CreateInTx(ctx, tx, inputs)
	})
}

// buildRows 校验并物化输入；空批次返回空切片（CreateInTx 视为 no-op）。
func buildRows(inputs []Input) ([]*model.Message, error) {
	rows := make([]*model.Message, 0, len(inputs))
	for i := range inputs {
		if err := ValidateInput(&inputs[i]); err != nil {
			return nil, err
		}
		rows = append(rows, &model.Message{
			UserID:      inputs[i].UserID,
			Type:        inputs[i].Type,
			Title:       inputs[i].Title,
			Content:     inputs[i].Content,
			BizType:     inputs[i].BizType,
			BizID:       inputs[i].BizID,
			Route:       inputs[i].Route,
			RouteParams: inputs[i].RouteParams,
		})
	}
	return rows, nil
}

// PageMy 当前用户的通知分页（严格本人：user_id = 登录主体；租户条件由插件
// 按 ctx 追加）。Type/Read 为可选过滤。
func (s *Service) PageMy(ctx context.Context, req *dto.MessagePageReq) ([]model.Message, int64, error) {
	userID, err := requireUser(ctx)
	if err != nil {
		return nil, 0, err
	}
	if req.Type != "" && !model.ValidType(req.Type) {
		return nil, 0, xerr.BadRequest("通知类型非法：" + req.Type)
	}
	req.Normalize()
	return s.repo.PageByUser(ctx, userID, req)
}

// CountUnread 当前用户未读数（顶栏铃铛 60 秒轮询消费，spec §4.13）。
func (s *Service) CountUnread(ctx context.Context) (int64, error) {
	userID, err := requireUser(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.CountUnread(ctx, userID)
}

// MarkRead 单条标记已读（幂等）：未读置读；已读重复标记视为成功；
// 非本人/不存在/已删除返回 404（本人判定在 SQL 谓词里，不依赖先查后改）。
func (s *Service) MarkRead(ctx context.Context, id int64) error {
	userID, err := requireUser(ctx)
	if err != nil {
		return err
	}
	affected, err := s.repo.MarkRead(ctx, userID, id, time.Now())
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	// 0 行受影响：区分「已读过（幂等成功）」与「非本人/不存在（404）」。
	n, err := s.repo.CountByUserAndID(ctx, userID, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return xerr.NotFound(fmt.Sprintf("通知 %d 不存在", id))
	}
	return nil
}

// MarkAllRead 全部标记已读（幂等：UPDATE 天然只作用于 read_at IS NULL 的行）。
func (s *Service) MarkAllRead(ctx context.Context) error {
	userID, err := requireUser(ctx)
	if err != nil {
		return err
	}
	_, err = s.repo.MarkAllRead(ctx, userID, time.Now())
	return err
}
