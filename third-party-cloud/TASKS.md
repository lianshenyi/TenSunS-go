# TASKS — 第三方手工录入主机

> 任务按阶段排；同阶段中标为可并行的项可同时推进。估算单位：⏱ ~30min | ⏱⏱ ~2h | ⏱⏱⏱ ~半天。
> 环境速查：Go API 默认 `127.0.0.1:2026`；Vue 开发服务由 `make web-dev` 启动，默认代理 `/api` 到该地址；Consul 由 `cmd/consulmock` 或 Compose 提供。

---

## Phase 0 — 契约确认

- [x] **0.1** 锁定内部 vendor 键 `third_party`、独立 API 前缀和不创建 scheduler job 的边界。
- [x] **0.2** 锁定账户、主机、Consul KV/Service 模型，字段校验、鉴权、响应码及编辑/删除语义。
- [x] **0.3** 将 API 契约发给 Vue 2 + Element UI 实现方，供其使用。

**Phase 0 完成标志**：`third-party-cloud/{PRD,spec,TASKS}.md` 可独立指导前后端实现，无接口字段 TBD。

## Phase 1 — Go API 与存储 ⏱⏱⏱

- [ ] **1.1** 添加 `third_party` 配置展示名、路由和受鉴权 handler，不影响遗留接口。
- [ ] **1.2** 实现账户 CRUD 与账户路径、重复账户、非空删除校验。
- [ ] **1.3** 实现主机 CRUD、严格 JSON 解码、UUID、IPv4/IPv6、端口、规格、状态和每账户 IP 唯一性校验。
- [ ] **1.4** 依第 6 节读写 KV/Service，覆盖创建、更新、删除和异常补偿路径。
- [ ] **1.5** 添加表驱动 httptest/Consul mock 测试：认证、成功 CRUD、每种校验失败、冲突、账户删除和 Service 映射。

**Phase 1 完成标志**：`go test ./internal/httpapi ./internal/...` 覆盖上述 API，主机可在 mock Consul 被发现。

## Phase 2 — Vue 2 + Element UI 界面 ⏱⏱

- [ ] **2.1** 增加第三方入口、API client 与路由，沿用 `request-ops` 的 Bearer Token 处理。
- [ ] **2.2** 实现账户选择/创建/删除和空账户保护提示。
- [ ] **2.3** 实现主机列表与新增/编辑/删除表单；字段校验必须与 spec 第 7.2 节一致。
- [ ] **2.4** 显示后端 `40000` / `50000` 错误，不把失败当成功；补前端构建验证。

**Phase 2 完成标志**：浏览器可完成账户和单台主机完整 CRUD，刷新后数据保持。

## Phase 3 — 联调、文档与回归 ⏱⏱

- [ ] **3.1** 用真实 Consul Compose 环境执行 API curl 联调：账户 → IPv4 主机 → IPv6 主机 → 编辑 → 删除。
- [ ] **3.2** 验证 KV、Service、IPv6 `instance` 与 TCP check 映射，并记录证据。
- [ ] **3.3** 运行 `go build ./...`、`go vet ./...`、`go test ./...` 以及前端构建；更新根目录 `RUNBOOK.md` 的功能状态、接口样例与排障说明。
- [ ] **3.4** 前后端互相验收后再发起独立 QA；本阶段之前不得触发独立 QA。

**Phase 3 完成标志**：前后端联调、构建和回归均通过，RUNBOOK 可复现环境和验收步骤。

## 后续阶段（不在 MVP / 可选增强）

- [ ] 批量 CSV 导入与导出。
- [ ] 将已批准的第三方主机纳入 JumpServer 同步的单独设计与实现。
- [ ] 连通性探测、标签和细粒度权限。

---

## 进度跟踪

| Phase | 状态 | 完成日期 |
|---|---|---|
| 0 契约 | ✅ | 2026-11-02 |
| 1 Go API 与存储 | ⬜ | |
| 2 Vue 2 界面 | ⬜ | |
| 3 联调、文档与回归 | ⬜ | |

---

## 速查命令

```bash
# 全量 Go 编译、静态检查和测试
go build ./...
go vet ./...
go test ./...

# 既有兼容冒烟（启动内存 Consul 与 API）
make smoke

# 启动 Vue 开发服务；如需改 API 地址可设置 TENSUNS_API
make web-dev

# Compose 环境
make env-init
make up
make ps
```
