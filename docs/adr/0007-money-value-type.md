# CRM 金额统一使用以分为单位的 Money 值类型

**Status**: accepted

CRM 的产品价格、商机预计金额、合同金额、回款计划与回款记录金额统一使用 `internal/pkg/money.Money`，以 int64 分为单位存储与运算，落库列为 `decimal(12,2)`，包内禁止 float64 参与金额换算。原因：float64 在大额合同与多次回款累加时会出现 0.01 级别的精度漂移，财务对账不可接受；int64 分是精确整数，加/减（`Add`/`Sub`）永远不丢分。代价：与浮点习惯代码互转要显式经过字符串，换来的不变量是「Money 里的值永远是精确的两位小数」。

**边界与格式**：解析（`ParseMoney`/`UnmarshalText`/`Scan`）只接受十进制字符串，小数最多两位、不四舍五入，超出即拒绝（`ErrInvalidFormat`）；范围锁定 `decimal(12,2)`——最大 9,999,999,999.99（分值上限 999999999999，含负对称），越界拒绝（`ErrOutOfRange`）。格式化（`String`/`MarshalText`/`MarshalJSON`）固定输出两位小数。

**JSON 契约**：`MarshalJSON` 输出十进制字符串（如 `"1234.56"`）；`UnmarshalJSON` 采用保守做法——只接受字符串字面量，JSON 数字字面量一律拒绝。原因：JavaScript 端把数字解析成 double 会先丢精度，前端必须以字符串承载金额；拒绝数字字面量可以在 API 边界就把隐患拦下，而不是默默接受一个已经漂移过的值。

**GORM 契约**：`Value` 输出十进制字符串写入 `decimal(12,2)` 列；`Scan` 常规路径只处理驱动返回的 `[]byte`/`string`（MySQL 驱动默认把 DECIMAL 读成字节串）。float64 仅作为少数驱动配置的兼容入口：立即转精确十进制字符串再走常规解析，有损值被两位小数校验拒绝，float64 不参与任何金额运算。

**Excel 契约**：Money 实现 `encoding.TextMarshaler`/`TextUnmarshaler`，`internal/pkg/excel` 的导入、导出与模板开箱即用，无需逐字段写转换函数；非法金额（如 `1234.567`）在导入阶段即返回行级明细错误。

**限制**：不支持多币种与汇率换算（超出本期范围）；`FromCents` 面向已知来源的分值、不做范围校验，外部构造金额一律走解析入口；需要乘除法（如单价 × 数量）时在业务层以分做整数运算后用 `FromCents` 收口，并由调用方保证结果在 `decimal(12,2)` 范围内。
