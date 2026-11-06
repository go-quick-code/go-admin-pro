// sensitive_word.go 承载敏感词的业务编排（issue #81，docs/platform-mvp-spec.md
// §3.8/§4.7/§5.6）：租户级词库 CRUD、单租户上限、按租户的匹配缓存与 CRUD 失效、
// 检测端点与供 OA/CRM 接入的检查入口。
//
// 匹配核心是 rune trie（见 sensitive_word_trie.go）：按租户缓存「启用词」构建
// 的 trie 快照（构建完成后只读，天然并发安全），CRUD 后删除对应租户的缓存键
// 失效（spec §4.7.3）。停用词不参与匹配——缓存只装启用词，改状态即失效重建。
//
// 词库读写在当前租户上下文内进行（system_sensitive_word 登记 TenantTables，
// 租户插件自动限定），跨租户业务 ID 一律 404（spec §4.11.4）。
package service

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 敏感词维护的取值边界（spec §4.7.1）。
const (
	// MaxSensitiveWordLen 是单词最大字符数（rune 计，中文算 1）。
	MaxSensitiveWordLen = 50
	// MaxSensitiveWordsPerTenant 是单租户词库上限。
	MaxSensitiveWordsPerTenant = 5000
)

// SensitiveWordService 汇聚敏感词能力；独立于主 Service（同 SocialService 的
// 取舍）：持有按租户的匹配缓存，不污染既有服务的缓存字段。
type SensitiveWordService struct {
	repo *repository.Repository

	// matchers 按租户缓存启用词的 trie 快照：map[tenantID]*sensitiveTrie。
	// LoadOrStore 允许并发首查重复构建（幂等、无副作用），CRUD 走 Delete 失效。
	matchers sync.Map
}

// NewSensitiveWordService 创建敏感词服务。
func NewSensitiveWordService(repo *repository.Repository) *SensitiveWordService {
	return &SensitiveWordService{repo: repo}
}

// ---------- 词库 CRUD ----------

// PageSensitiveWords 词库分页（当前租户）。
func (s *SensitiveWordService) PageSensitiveWords(ctx context.Context, req *dto.SensitiveWordPageReq) ([]model.SensitiveWord, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.SensitiveWordFilter{
		Word:     strings.TrimSpace(req.Word),
		Category: strings.TrimSpace(req.Category),
		Status:   req.Status,
	}
	return s.repo.ListSensitiveWords(ctx, filter, page.Offset(), page.PageSize)
}

// CreateSensitiveWord 创建敏感词：去空白 1–50 字符、租户内唯一（409）、
// 单租户上限 5000（409）；成功后失效匹配缓存。
func (s *SensitiveWordService) CreateSensitiveWord(ctx context.Context, req *dto.SensitiveWordCreateReq) error {
	word, category, status, remark, err := validateSensitiveWordFields(req.Word, req.Category, req.Status, req.Remark)
	if err != nil {
		return err
	}
	if err := s.ensureWordFree(ctx, word, 0); err != nil {
		return err
	}
	if err := s.ensureUnderLimit(ctx); err != nil {
		return err
	}
	if err := s.repo.CreateSensitiveWord(ctx, &model.SensitiveWord{
		Word:     word,
		Category: category,
		Status:   status,
		Remark:   remark,
	}); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// UpdateSensitiveWord 修改敏感词；跨租户/不存在 404；改词/改状态均失效缓存。
func (s *SensitiveWordService) UpdateSensitiveWord(ctx context.Context, req *dto.SensitiveWordUpdateReq) error {
	row, err := s.repo.FindSensitiveWordByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("敏感词不存在")
	}
	word, category, status, remark, err := validateSensitiveWordFields(req.Word, req.Category, req.Status, req.Remark)
	if err != nil {
		return err
	}
	if err := s.ensureWordFree(ctx, word, req.ID); err != nil {
		return err
	}
	row.Word = word
	row.Category = category
	row.Status = status
	row.Remark = remark
	if err := s.repo.SaveSensitiveWord(ctx, row); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// DeleteSensitiveWord 软删敏感词；跨租户/不存在 404；失效缓存。
func (s *SensitiveWordService) DeleteSensitiveWord(ctx context.Context, id int64) error {
	row, err := s.repo.FindSensitiveWordByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("敏感词不存在")
	}
	if err := s.repo.DeleteSensitiveWord(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// validateSensitiveWordFields 归一化并校验词库字段（spec §4.7.1：
// 去空白后非空、1–50 字符；状态 0 启用 / 1 停用）。
func validateSensitiveWordFields(rawWord, rawCategory string, status int8, rawRemark string) (string, string, int8, string, error) {
	word := strings.TrimSpace(rawWord)
	if word == "" {
		return "", "", 0, "", xerr.BadRequest("敏感词不能为空")
	}
	if utf8.RuneCountInString(word) > MaxSensitiveWordLen {
		return "", "", 0, "", xerr.BadRequest("敏感词长度不能超过 50 个字符")
	}
	if status != model.StatusEnable && status != model.StatusDisable {
		return "", "", 0, "", xerr.BadRequest("状态取值应为 0 启用 / 1 停用")
	}
	return word, strings.TrimSpace(rawCategory), status, strings.TrimSpace(rawRemark), nil
}

// ensureWordFree 校验租户内词唯一（软删行不计入，deleted_at 入唯一键）。
func (s *SensitiveWordService) ensureWordFree(ctx context.Context, word string, excludeID int64) error {
	count, err := s.repo.CountSensitiveWordsByWord(ctx, word, excludeID)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.Conflict("敏感词已存在：" + word)
	}
	return nil
}

// ensureUnderLimit 校验单租户词库上限（spec §4.7.1：上限 5000）。
func (s *SensitiveWordService) ensureUnderLimit(ctx context.Context) error {
	count, err := s.repo.CountSensitiveWords(ctx)
	if err != nil {
		return err
	}
	if count >= MaxSensitiveWordsPerTenant {
		return xerr.Conflict("敏感词数量已达上限（5000），请先删除部分词条")
	}
	return nil
}

// ---------- 匹配缓存与检测 ----------

// matcherOf 取当前租户的 trie 快照：缓存命中直接返回；未命中从库加载启用词
// 构建后回填（并发首查允许重复构建，LoadOrStore 保证最终只有一个快照生效）。
func (s *SensitiveWordService) matcherOf(ctx context.Context) (*sensitiveTrie, error) {
	tenantID := tenant.ID(ctx)
	if v, ok := s.matchers.Load(tenantID); ok {
		return v.(*sensitiveTrie), nil
	}
	rows, err := s.repo.ListEnabledSensitiveWords(ctx)
	if err != nil {
		return nil, err
	}
	trie := newSensitiveTrie()
	for i := range rows {
		trie.Insert(rows[i].Word)
	}
	actual, _ := s.matchers.LoadOrStore(tenantID, trie)
	return actual.(*sensitiveTrie), nil
}

// invalidate 失效当前租户的匹配缓存（CRUD 后调用，spec §4.7.3）。
func (s *SensitiveWordService) invalidate(ctx context.Context) {
	s.matchers.Delete(tenant.ID(ctx))
}

// CheckSensitiveText 检测文本（检测端点用）：返回去重后的命中词（按首次
// 出现位置排序）与打码文本（命中字符替换 *，spec §4.7.2）。
func (s *SensitiveWordService) CheckSensitiveText(ctx context.Context, text string) (*dto.SensitiveWordCheckResp, error) {
	trie, err := s.matcherOf(ctx)
	if err != nil {
		return nil, err
	}
	words, masked := trie.Scan(text)
	if words == nil {
		words = []string{}
	}
	return &dto.SensitiveWordCheckResp{Words: words, Masked: masked}, nil
}

// CheckSensitiveWords 供业务模块接入的检查入口（经 internal/app 注入 OA/CRM
// 的 SensitiveWordChecker 窄接口，spec §4.7.4）：依次检测多段文本（公告 =
// 标题+正文，跟进 = 内容），任一命中即返回 400「内容包含敏感词：a、b」，
// 由调用方在落库前调用，命中时不写业务数据。
func (s *SensitiveWordService) CheckSensitiveWords(ctx context.Context, texts ...string) error {
	trie, err := s.matcherOf(ctx)
	if err != nil {
		return err
	}
	var hits []string
	for _, text := range texts {
		words, _ := trie.Scan(text)
		hits = append(hits, words...)
	}
	if len(hits) > 0 {
		return xerr.BadRequest("内容包含敏感词：" + strings.Join(hits, "、"))
	}
	return nil
}
