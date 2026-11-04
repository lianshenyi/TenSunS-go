# TASKS — 第三方云厂商手工机器录入

> 环境速查：Go API 默认 `127.0.0.1:2026`；Vue 开发服务由 `make web-dev` 启动；Consul 由 `cmd/consulmock` 或 Compose 提供。

---

## Phase 0 — 契约确认（完成）

- [x] **0.1** 已确认产品边界：复用 `/api/edit/cloud`、现有鉴权、envelope、Consul KV / Service 注册模型；不新增独立 CRUD / KV。**不代表 Phase 0 完成。**
- [x] **0.2** 已确认产品字段规则：`third_party`、完整集合全量覆盖、名称与 `IP:端口` 唯一、名称/规格/IP/端口必填、账号/项目可选。**不代表 Phase 0 完成。**
- [x] **0.3** 在既有 `POST /api/edit/commit` 中落实 `vendor=third_party`、完整 `hosts` 集合与 `{count:N}` 响应；空账号映射 `default`、region 映射 `global`，不创建任务或独立模型。

**Phase 0 完成标志**：存在可实现、非独立的读写契约；不创建任务、不触发云同步。

## Phase 1 — Go 后端 ⏱⏱⏱

- [x] **1.1** 在确认的既有编辑 handler 中增加 `third_party` 完整集合解析和严格校验。
- [x] **1.2** 沿用既有 ECS Service 模型注册、更新和全量覆盖删除，且不写第三方 jobs / records。
- [x] **1.3** 补表驱动测试：认证、IPv4/IPv6、端口、必填、重复名称、重复 `IP:端口` 和全量删除。
- [x] **1.4** 扩展 `GET /api/edit/cloud?vendor=third_party`：从所有第三方 ECS Service 返回跨账户完整 `data.hosts` 基线，并回归验证读取后全量保存不会误删其他账户。

**Phase 1 完成标志**：确认的现有端点在 mock Consul 中可跨账户保存并读取第三方完整集合。

## Phase 2 — Vue 2 + Element UI ⏱⏱

- [x] **2.1** 在现有云资源编辑页面增加第三方厂商入口和机器集合编辑。
- [x] **2.2** 实现与契约一致的必填、IP、端口、重复项校验与后端错误展示。
- [x] **2.3** 使用确认的既有编辑请求完成全量保存；不调用独立 CRUD。
- [x] **2.4** 修复 BUG-003：375×812 下编辑弹窗使用可读的移动宽度，第三方表格保持列最小宽度并可横向滚动；完成加载、新增、保存和删除的隔离浏览器回归，桌面 1280px 布局保持 45% 宽度。
- [x] **2.5** 修复 BUG-004（关联 Phase 2 / VIDE-39）：新增云资源选择第三方时不得调用云账户创建接口；前端引导到完整主机集合编辑器，后端对旧页面或直调请求返回明确校验提示，且不创建第三方任务。

**Phase 2 完成标志**：浏览器可提交并刷新读取完整第三方机器集合。

## Phase 3 — 联调、文档与回归 ⏱⏱

- [x] **3.1** 在真实 Consul 环境验证 Service 写入、覆盖删除和 IPv6 地址格式；验证跨账户完整读取、`default/global` 规范化、IPv4/IPv6 TCP Service、account/project 保留，以及不创建第三方 jobs。
- [x] **3.2** 执行 `go build ./...`、`go vet ./...`、`go test ./...` 与前端构建。
- [x] **3.3** 更新 RUNBOOK，并提供 Go `httptest`、真实 Consul 和前端校验证据。

**Phase 3 完成标志**：前后端联调和回归通过，运行手册可复现。

## 进度跟踪

| Phase | 状态 | 完成日期 |
|---|---|---|
| 0 契约 | ✅ 完成：0.1–0.3 已确认 | 2026-11-03 |
| 1 Go 后端 | ✅ 完成 | 2026-11-03 |
| 2 Vue 2 界面 | ✅ 完成：2.1–2.4 已实现真实既有 API 联调及 BUG-003 移动端修复 | 2026-11-03 |
| 3 联调、文档与回归 | ✅ 完成：3.1–3.3 已闭环；功能待统一 QA 验收 | 2026-11-03 |

## 速查命令

```bash
go build ./...
go vet ./...
go test ./...
make smoke
make web-dev
```
