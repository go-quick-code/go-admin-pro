package dto

// VisibilityScopeResp 是可见范围探测接口（GET /oa/visibility-scope）的响应体。
//   - All 为 true：当前用户持有 oa:<resource>:manage（或 super_admin 通配），
//     全租户可见，UserIDs 无意义（恒为空数组）；
//   - 否则 UserIDs 是可见记录的归属用户集合（升序去重）：
//     部门负责人为其部门子树内用户 ∪ 本人，普通用户仅本人。
type VisibilityScopeResp struct {
	Resource string  `json:"resource"`
	All      bool    `json:"all"`
	UserIDs  []int64 `json:"userIds"`
}
