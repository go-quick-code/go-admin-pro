package codegen

import (
	"archive/zip"
	"bytes"
	"io"
	"text/template"
)

func mustRender(tmpl string, data templateData) string {
	t := template.Must(template.New("code").Delims("[[", "]]").Parse(tmpl))
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(err)
	}
	return buf.String()
}

type zipWriter struct {
	zw *zip.Writer
}

func newZipWriter(w io.Writer) *zipWriter {
	return &zipWriter{zw: zip.NewWriter(w)}
}

func (z *zipWriter) add(name string, content []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o644)
	w, err := z.zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

func (z *zipWriter) close() error {
	return z.zw.Close()
}

const tmplModel = `package model

import (
	"time"

	"gorm.io/gorm"
)

// [[.ClassName]] [[.TableComment]]
type [[.ClassName]] struct {
	ID int64 ` + "`" + `gorm:"primaryKey;autoIncrement" json:"id"` + "`" + `
[[range .Fields]]	[[.GoName]] [[.GoType]] ` + "`" + `gorm:"column:[[.ColumnName]]" json:"[[.JSONName]]"` + "`" + ` // [[.Comment]]
[[end]]	CreatedAt time.Time      ` + "`" + `json:"createTime"` + "`" + `
	UpdatedAt time.Time      ` + "`" + `json:"updateTime"` + "`" + `
	DeletedAt gorm.DeletedAt ` + "`" + `gorm:"index" json:"-"` + "`" + `
}

func ([[.ClassName]]) TableName() string {
	return "[[.TableName]]"
}
`

const tmplDTO = `package dto

// [[.ClassName]]CreateReq 新增请求。
type [[.ClassName]]CreateReq struct {
[[range .Fields]]	[[.GoName]] [[if .IsTime]]string[[else]][[.GoType]][[end]] ` + "`" + `json:"[[.JSONName]]"` + "`" + `
[[end]]}

// [[.ClassName]]UpdateReq 修改请求。
type [[.ClassName]]UpdateReq struct {
	ID int64 ` + "`" + `json:"id" binding:"required"` + "`" + `
[[range .Fields]]	[[.GoName]] [[if .IsTime]]string[[else]][[.GoType]][[end]] ` + "`" + `json:"[[.JSONName]]"` + "`" + `
[[end]]}

// [[.ClassName]]PageReq 分页查询请求。
type [[.ClassName]]PageReq struct {
	PageNo   int ` + "`" + `form:"pageNo"` + "`" + `
	PageSize int ` + "`" + `form:"pageSize"` + "`" + `
[[range .SearchFields]]	[[.GoName]] string ` + "`" + `form:"[[.JSONName]]"` + "`" + `
[[end]]}
`

const tmplService = `package service

import (
	"context"
[[if .HasTime]]
	"strings"
	"time"
[[end]]
	"github.com/lautung/go-admin-pro/internal/module/[[.ModuleName]]/dto"
	"github.com/lautung/go-admin-pro/internal/module/[[.ModuleName]]/model"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"gorm.io/gorm"
)

// [[.ClassName]]Service [[.TableComment]]业务逻辑。
type [[.ClassName]]Service struct {
	db *gorm.DB
[[if .HasTime]]	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入；
	// nil 表示未注入（退回 time.Local，见 businessLocation）。
	location *time.Location
[[end]]}

func New[[.ClassName]]Service(db *gorm.DB) *[[.ClassName]]Service {
	return &[[.ClassName]]Service{db: db}
}
[[if .HasTime]]
// SetLocation 注入业务时区（ADR 0006：唯一来源是 config.Location()）。nil 忽略。
func (s *[[.ClassName]]Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区；未注入时退回 time.Local（测试/工具场景）。
func (s *[[.ClassName]]Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}
[[end]]

// List 分页查询。
func (s *[[.ClassName]]Service) List(ctx context.Context, req *dto.[[.ClassName]]PageReq) ([]model.[[.ClassName]], int64, error) {
	q := s.db.WithContext(ctx).Model(&model.[[.ClassName]]{})
[[range .SearchFields]]	if req.[[.GoName]] != "" {
		q = q.Where("[[.ColumnName]] LIKE ?", "%"+req.[[.GoName]]+"%")
	}
[[end]]
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	var list []model.[[.ClassName]]
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Get 查询详情。
func (s *[[.ClassName]]Service) Get(ctx context.Context, id int64) (*model.[[.ClassName]], error) {
	var row model.[[.ClassName]]
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Create 新增。
func (s *[[.ClassName]]Service) Create(ctx context.Context, req *dto.[[.ClassName]]CreateReq) error {
	row := &model.[[.ClassName]]{
[[range .Fields]]		[[.GoName]]: [[if .IsTime]]s.parseOptionalTime(req.[[.GoName]])[[else]]req.[[.GoName]][[end]],
[[end]]	}
	return s.db.WithContext(ctx).Create(row).Error
}

// Update 修改。
func (s *[[.ClassName]]Service) Update(ctx context.Context, req *dto.[[.ClassName]]UpdateReq) error {
	row, err := s.Get(ctx, req.ID)
	if err != nil {
		return err
	}
[[range .Fields]]	row.[[.GoName]] = [[if .IsTime]]s.parseOptionalTime(req.[[.GoName]])[[else]]req.[[.GoName]][[end]]
[[end]]	return s.db.WithContext(ctx).Save(row).Error
}

// Delete 删除。
func (s *[[.ClassName]]Service) Delete(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Delete(&model.[[.ClassName]]{}, id).Error
}
[[if .HasTime]]
// parseOptionalTime 把前端提交的时间字符串解析为可空时间：空串返回 nil，
// 格式非法返回 nil（选填时间字段按无值处理）；按业务时区解释（ADR 0006）。
func (s *[[.ClassName]]Service) parseOptionalTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, value, s.businessLocation()); err == nil {
			return &t
		}
	}
	return nil
}
[[end]]
`

const tmplHandler = `package api

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/[[.ModuleName]]/dto"
	"github.com/lautung/go-admin-pro/internal/module/[[.ModuleName]]/service"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// [[.ClassName]]Handler [[.TableComment]]接口。
type [[.ClassName]]Handler struct {
	svc *service.[[.ClassName]]Service
}

func New[[.ClassName]]Handler(svc *service.[[.ClassName]]Service) *[[.ClassName]]Handler {
	return &[[.ClassName]]Handler{svc: svc}
}

func (h *[[.ClassName]]Handler) Page(c *gin.Context) {
	var req dto.[[.ClassName]]PageReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return
	}
	list, total, err := h.svc.List(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *[[.ClassName]]Handler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "非法的 ID")
		return
	}
	row, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

func (h *[[.ClassName]]Handler) Create(c *gin.Context) {
	var req dto.[[.ClassName]]CreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return
	}
	if err := h.svc.Create(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *[[.ClassName]]Handler) Update(c *gin.Context) {
	var req dto.[[.ClassName]]UpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return
	}
	if err := h.svc.Update(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *[[.ClassName]]Handler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "非法的 ID")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// Register[[.ClassName]] 注册路由，可在 router 中按需调用。
func Register[[.ClassName]](rg *gin.RouterGroup, perm func(string) gin.HandlerFunc, svc *service.[[.ClassName]]Service) {
	h := New[[.ClassName]]Handler(svc)
	group := rg.Group("/[[.ModuleName]]/[[.BusinessName]]")
	group.GET("/page", perm("[[.ModuleName]]:[[.BusinessName]]:query"), h.Page)
	group.GET("/:id", perm("[[.ModuleName]]:[[.BusinessName]]:query"), h.Get)
	group.POST("", perm("[[.ModuleName]]:[[.BusinessName]]:create"), h.Create)
	group.PUT("", perm("[[.ModuleName]]:[[.BusinessName]]:update"), h.Update)
	group.DELETE("/:id", perm("[[.ModuleName]]:[[.BusinessName]]:delete"), h.Delete)
}
`

const tmplAPI = `import request from '@/utils/request'

export interface [[.ClassName]] {
  id?: number
[[range .Fields]]  [[.JSONName]]?: [[.TSType]]
[[end]]  createTime?: string
}

export function page[[.ClassName]](params: any) {
  return request.get('/[[.ModuleName]]/[[.BusinessName]]/page', { params })
}

export function get[[.ClassName]](id: number) {
  return request.get(` + "`" + `/[[.ModuleName]]/[[.BusinessName]]/${id}` + "`" + `)
}

export function create[[.ClassName]](data: [[.ClassName]]) {
  return request.post('/[[.ModuleName]]/[[.BusinessName]]', data)
}

export function update[[.ClassName]](data: [[.ClassName]]) {
  return request.put('/[[.ModuleName]]/[[.BusinessName]]', data)
}

export function delete[[.ClassName]](id: number) {
  return request.delete(` + "`" + `/[[.ModuleName]]/[[.BusinessName]]/${id}` + "`" + `)
}
`

const tmplVue = `<template>
  <div class="app-container">
    <el-card shadow="never">
      <el-form :inline="true" :model="query" class="search-form">
[[range .SearchFields]]        <el-form-item label="[[.Comment]]">
          <el-input v-model="query.[[.JSONName]]" placeholder="请输入[[.Comment]]" clearable @keyup.enter="handleSearch" />
        </el-form-item>
[[end]]        <el-form-item>
          <el-button type="primary" @click="handleSearch">查询</el-button>
          <el-button @click="handleReset">重置</el-button>
        </el-form-item>
      </el-form>

      <div class="toolbar">
        <el-button type="primary" @click="openCreate">新增</el-button>
      </div>

      <el-table v-loading="loading" :data="list" border stripe>
[[range .Fields]]        <el-table-column prop="[[.JSONName]]" label="[[.Comment]]" min-width="120" show-overflow-tooltip />
[[end]]        <el-table-column prop="createTime" label="创建时间" min-width="170" />
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="query.pageNo"
        v-model:page-size="query.pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="loadList"
        @current-change="loadList"
      />
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="620px" destroy-on-close>
      <el-form :model="form" label-width="120px">
[[range .Fields]]        <el-form-item label="[[.Comment]]">
[[if eq .FormType "textarea"]]          <el-input v-model="form.[[.JSONName]]" type="textarea" :rows="3" />
[[else if eq .FormType "number"]]          <el-input-number v-model="form.[[.JSONName]]" :min="0" style="width: 100%" />
[[else if eq .FormType "datetime"]]          <el-date-picker v-model="form.[[.JSONName]]" type="datetime" value-format="YYYY-MM-DD HH:mm:ss" style="width: 100%" />
[[else]]          <el-input v-model="form.[[.JSONName]]" />
[[end]]        </el-form-item>
[[end]]      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  page[[.ClassName]],
  create[[.ClassName]],
  update[[.ClassName]],
  delete[[.ClassName]],
} from '@/api/[[.ModuleName]]/[[.BusinessName]]'

const loading = ref(false)
const submitting = ref(false)
const list = ref<any[]>([])
const total = ref(0)
const dialogVisible = ref(false)
const dialogTitle = ref('新增')
const editingId = ref<number | null>(null)

const query = reactive<any>({
  pageNo: 1,
  pageSize: 10,
[[range .SearchFields]]  [[.JSONName]]: '',
[[end]]})
const form = reactive<any>({})

function defaultForm() {
  const data: any = {}
[[range .Fields]]  data.[[.JSONName]] = [[.ZeroValue]]
[[end]]  return data
}

function resetForm() {
  Object.assign(form, defaultForm())
}

async function loadList() {
  loading.value = true
  try {
    const data: any = await page[[.ClassName]]({ ...query })
    list.value = data.list || []
    total.value = data.total || 0
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  query.pageNo = 1
  loadList()
}

function handleReset() {
[[range .SearchFields]]  query.[[.JSONName]] = ''
[[end]]  query.pageNo = 1
  loadList()
}

function openCreate() {
  editingId.value = null
  dialogTitle.value = '新增'
  resetForm()
  dialogVisible.value = true
}

function openEdit(row: any) {
  editingId.value = row.id
  dialogTitle.value = '修改'
  resetForm()
  Object.assign(form, row)
  dialogVisible.value = true
}

async function handleSubmit() {
  submitting.value = true
  try {
    if (editingId.value) {
      await update[[.ClassName]]({ ...form, id: editingId.value })
    } else {
      await create[[.ClassName]]({ ...form })
    }
    ElMessage.success('保存成功')
    dialogVisible.value = false
    loadList()
  } finally {
    submitting.value = false
  }
}

async function handleDelete(row: any) {
  await ElMessageBox.confirm('确认删除「' + row.id + '」吗？', '提示', { type: 'warning' })
  await delete[[.ClassName]](row.id)
  ElMessage.success('删除成功')
  loadList()
}

onMounted(loadList)
</script>

<style scoped>
.search-form {
  margin-bottom: 4px;
}

.toolbar {
  margin-bottom: 12px;
}

.pager {
  margin-top: 16px;
  justify-content: flex-end;
}
</style>
`

const tmplReadme = `# [[.ClassName]] 代码生成结果

- 表名：[[.TableName]]
- 表注释：[[.TableComment]]
- 模块：[[.ModuleName]]
- 业务名：[[.BusinessName]]
- 生成人：[[.Author]]

## 接入步骤

1. 将 ` + "`go/`" + ` 目录下的 model、dto、service、api 拷贝到 ` + "`internal/module/[[.ModuleName]]/`" + ` 对应目录。
2. 将 ` + "`web/`" + ` 目录下的文件拷贝到前端对应目录。
3. 在路由中注册接口，例如：

` + "```go" + `
[[.VarName]]Svc := service.New[[.ClassName]]Service(db)
api.Register[[.ClassName]](protectedGroup, systemHandler.RequirePermission, [[.VarName]]Svc)
` + "```" + `

4. 在「系统管理 → 菜单管理」中新增菜单与按钮，权限标识使用：

- [[.ModuleName]]:[[.BusinessName]]:query
- [[.ModuleName]]:[[.BusinessName]]:create
- [[.ModuleName]]:[[.BusinessName]]:update
- [[.ModuleName]]:[[.BusinessName]]:delete

5. 执行 ` + "`go build ./...`" + ` 验证编译。

> 生成的代码是起点，请根据业务补充校验、唯一性检查与数据权限。
`
