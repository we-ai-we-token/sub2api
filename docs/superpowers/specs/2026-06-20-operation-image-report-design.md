# 运营管理 / 生图报表 — 设计文档

- 日期：2026-06-20
- 状态：已确认，待实现
- 范围：在管理员后台新增一级菜单「运营管理」，并实现其第一个二级菜单「生图报表」

## 1. 目标与边界

新增一个**与现有业务逻辑解耦**的运营管理模块。首个功能是「生图报表」，对 OpenAI（`gpt-image-2*`）与 Gemini（`gemini-*`）生图调用做运营监控与统计展示。

隔离策略（采用方案 A：独立子包 + 复用基础设施）：

- **纯只读**：仅 `SELECT` 现有表（`usage_logs` / `accounts` / `groups`）+ 读 Redis 实时并发计数；不写库、不调用任何现有网关/计费/账号业务方法、不改动现有写入路径。
- **健康的基础设施复用**：共用同一 Go binary 的 DB 连接池、Redis 客户端、Gin 路由树、admin 鉴权中间件；前端共用同一 Vue SPA 的路由、侧边栏、API client。
- **代码自成一片**：新增代码集中在 `operation` 专属目录/文件；对现有文件仅有 3 处各 1 行注册（路由、前端路由、菜单）。
- **可整体摘除**：删除该模块 = 删除 operation 目录 + 撤销 3 行注册。
- 本期**不新建 `operation_*` 表**（生图报表全只读）。目录结构按"未来会有独立运营表"预留，后续运营功能可直接新增 `operation_*` 前缀的表。

## 2. 数据来源（已核实）

| 数据 | 位置 | 说明 |
|---|---|---|
| 生图调用明细 | `usage_logs` 表 | 通过 `image_count > 0` 框定生图请求 |
| 时间戳 | `usage_logs.created_at` | 用于时区切桶 |
| 模型名 | `usage_logs.model` | 直接取用，兼容 `gpt-image-2*` 变体与各 `gemini-*-image` |
| 耗时 | `usage_logs.duration_ms` | 成功耗时曲线用 |
| 分组 | `usage_logs.group_id` | 可空 |
| 账号 | `usage_logs.account_id` → `accounts` | 关联取平台 |
| 平台 | `accounts.platform` | `openai` / `gemini` |
| 成功/失败判定 | `usage_logs.actual_cost` | 项目既有约定：`> 0` 成功，`= 0` 失败（失败写占位记录）。沿用此口径，与现有 Dashboard 一致 |
| 账号并发上限 | `accounts.concurrency` | 默认 3 |
| 实时并发 | Redis `concurrency:account:{id}` | 经 `ConcurrencyService.GetAccountConcurrency()` 读取 |
| 5h/7d 使用率 | `accounts.extra` (JSONB) | key：`codex_5h_used_percent`、`codex_7d_used_percent`（仅 openai oauth）|

可用索引：`usage_logs(created_at)`、`(account_id, created_at)`、`(group_id, created_at)`、`(model)`。

## 3. 已确认的口径决策

1. **告警并发卡片**（仅 openai）：对 5h 或 7d 使用率 ≥ 90% 的 openai oauth 账号，**同时展示**这批账号的「实时并发之和」与「配置上限之和」。
2. **成功/失败判定**：沿用 `actual_cost > 0` 约定，不新增字段、不改写入逻辑。
3. **查询方式**：实时只读查 `usage_logs`（近 24h、5min 桶最多 288 点），不建预聚合表。
4. **时区**：跟随管理员浏览器时区（前端在请求中传 IANA 时区，后端据此 `date_trunc` / `date_bin`）。

## 4. 后端设计

### 4.1 新增文件

```
backend/internal/service/operation/image_report_service.go   # operation 子包（新建）
backend/internal/handler/admin/operation_image_report_handler.go
backend/internal/server/routes/admin_operation.go            # registerAdminOperationRoutes
```

### 4.2 现有文件接触点（仅注册）

- `backend/internal/server/routes/admin.go`：在 admin 路由组注册处新增一行 `registerAdminOperationRoutes(admin, h)`。
- handler 聚合结构体：挂一个 `Operation` 字段指向新 handler（参照现有 `h.Admin.*` 组织方式）。

### 4.3 API 端点

前缀 `/api/v1/admin/operation/image-report`，全部 `GET`，复用现有 admin 鉴权中间件。

| 端点 | 用途 | 需求 |
|---|---|---|
| `GET /overview` | 模型范围概览 + 今日看板（平台/模型/分组 × 成功/失败） | 1、6 |
| `GET /concurrency` | 并发卡片（openai/gemini 当前/总并发）+ 告警并发 | 2 |
| `GET /latency-series` | 成功耗时曲线（min/p25/p50/p75/max/avg） | 3、5 |
| `GET /request-series` | 请求量曲线（成功数/失败数/成功率） | 4、5 |
| `GET /filters` | 可选模型列表、分组列表（筛选下拉用） | 5 |

公共筛选参数：

- `platform`：`openai`（默认）/ `gemini`
- `model`：可选，精确模型名
- `group_id`：可选
- `bucket`：`5m` / `1h`
- `tz`：IANA 时区字符串（前端传）

### 4.4 查询逻辑

**生图范围**：`image_count > 0`；平台由 `accounts.platform` 决定；模型名取 `usage_logs.model`。

1. **今日看板（/overview）**：按管理员时区切「今天」，用 `GROUP BY GROUPING SETS` 一次产出 平台 / 模型 / 分组 三个维度，每维度再按 `actual_cost > 0` 拆 成功 / 失败 计数。

2. **并发卡片（/concurrency）**：
   - 当前并发（分平台）：取该平台 active 且参与生图的账号，逐个 `GetAccountConcurrency()`(Redis) 求和。
   - 总并发（分平台）：`SUM(accounts.concurrency)`。
   - 告警并发（仅 openai）：筛
     `platform='openai' AND type='oauth' AND ((extra->>'codex_5h_used_percent')::float >= 90 OR (extra->>'codex_7d_used_percent')::float >= 90)`
     的账号，输出其「实时并发之和」与「配置上限之和」。

3. **成功耗时曲线（/latency-series）**：仅成功（`actual_cost > 0`）且 `image_count > 0`，按 `bucket` 用 PG15 `date_bin` 切桶（先 `created_at AT TIME ZONE $tz` 转本地再 bin），每桶用 `percentile_cont` 求 min/p25/p50/p75/max，并取 `avg(duration_ms)`；窗口最多近 24h。

4. **请求量曲线（/request-series）**：同窗口同桶，每桶
   `COUNT(*) FILTER (WHERE actual_cost > 0)` 成功数、
   `COUNT(*) FILTER (WHERE actual_cost = 0)` 失败数、
   成功率 = 成功 /（成功 + 失败）。

3、4 均支持 `platform`/`model`/`group_id` 筛选。

## 5. 前端设计

### 5.1 新增文件

```
frontend/src/api/admin/operationImageReport.ts            # API 封装
frontend/src/views/admin/operation/ImageReportView.vue    # 页面
```

### 5.2 现有文件接触点

- `frontend/src/api/admin/index.ts`：import + 挂到 `adminAPI.operationImageReport`。
- `frontend/src/router/index.ts`：新增 `/admin/operation/image-report` 路由（`requiresAuth + requiresAdmin`）。
- `frontend/src/components/layout/AppSidebar.vue`：新增一级菜单「运营管理」（`expandOnly`）+ 子项「生图报表」。
- `frontend/src/i18n/locales/zh.ts` 与 `en.ts`：新增菜单与页面文案。

### 5.3 页面布局（`ImageReportView.vue`）

自上而下：

1. **并发卡片行**：openai / gemini 各一张（当前并发 / 总并发），openai 额外一张告警并发卡（实时并发之和 / 配置上限之和）。
2. **今日看板**：平台 / 模型 / 分组三组卡片或表，每项分 成功 / 失败。
3. **筛选栏**：平台（默认 openai）/ 模型 / 分组 / 粒度（5m·1h）。
4. **图表区**：成功耗时曲线 + 请求量曲线。

图表库使用项目现有 **ECharts**；耗时图参照 `views/admin/ops/components/OpsLatencyChart.vue`，请求量趋势图参照 `OpsThroughputTrendChart.vue`。时区参数取浏览器 `Intl.DateTimeFormat().resolvedOptions().timeZone`，随请求下发。

## 6. 错误处理与边界

- Redis 读并发失败：对应平台「当前并发」显示「暂不可用」，不阻断整页其余数据。
- 账号 `extra` 缺 5h/7d 字段：视为未达 90%，不计入告警并发。
- 空数据：曲线返回空桶数组，前端显示「暂无数据」。
- 所有查询带时间范围谓词，命中既有索引，避免全表扫描。
- 时区参数非法/缺失：后端回退到服务器时区并照常返回。

## 7. 测试

- **service 层**：成功/失败口径（`actual_cost`）、时区切桶（`date_bin` + `AT TIME ZONE`）、筛选组合（platform/model/group）、今日看板 GROUPING SETS、告警账号筛选（`extra` 阈值）。
- **并发卡片**：对 `ConcurrencyService` / Redis 做 mock，验证求和与告警筛选；Redis 失败降级。
- **前端**：API 封装与视图基本渲染/空数据态测试（参照现有 charts `__tests__` 写法）。

## 8. 分支与提交

当前位于 `release`，按 CLAUDE.md 不在 `release` 上开发。实现时从 `pre-release` 切出 feature 分支进行，验证后按既定流程合入 `pre-release` 再到 `release`。本设计文档可先行提交。
