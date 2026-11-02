# spec — 第三方云厂商手工机器录入技术设计

**产品形态**：TenSunS-go 既有云资源编辑模型中的 `third_party` 厂商扩展。
**状态**：设计稿 v1.4（2026-11-03）；Phase 0 已完成，进入实现。

---

## 1. 概述

- vendor 内部键固定为 `third_party`，展示名为“第三方”。
- 复用现有 `/api/edit/cloud`、Bearer 鉴权、响应 envelope、Consul KV 与 ECS Service 注册模型；`vendor=third_party` 时该读取端点返回跨账户完整 `data.hosts` 集合，不增加独立 CRUD 或 KV 前缀。
- 提交是完整集合的全量覆盖：新集合写入后，之前存在但本次缺失的机器被移除。
- 机器名称、规格、IP、端口必填；账号、项目归属可选。

## 2. 目标 / 非目标

### Goals

- 将第三方机器纳入既有云资源编辑与读取模型。
- 使用既有 Consul ECS Service 名称、Tags、Meta 与健康检查字段。
- 在保存前后端一致验证名称与 `IP:端口` 的全局唯一性。

### Non-Goals（明确不做）

- 不新增 `/api/third-party/*`，不创建独立账户 / 主机 CRUD。
- 不创建 `ConsulManager/assets/third_party/*` 或其他独立 KV 结构。
- 不创建 `ConsulManager/jobs/third_party/*`、同步记录或 JumpServer 同步。

## 3. 架构总览

浏览器沿用现有 Vue 2 编辑页和 Bearer Token，读取仍经 `GET /api/edit/cloud`。完整集合通过 `POST /api/edit/commit` 的 `vendor=third_party` 分支保存；后端以当前 ECS Service 注册模型写入，并将缺失的旧 Service 注销。现有 `/api/nodes/res` 通过 Service 名称和 Tags 读取这些资源。

## 4. 系统组件 / 模块

### 4.1 Vue 2 + Element UI 编辑页

职责：增加 `third_party` 选项、维护可增删的机器行，并在提交前执行 §7 校验；不引入独立页面或 API client。

### 4.2 `internal/httpapi` 编辑 handler

职责：保持 `/api/edit/cloud` 的读语义和鉴权；在确认的既有写端点上解析完整机器集合、校验并交由 Consul Service 保存。

### 4.3 Consul ECS Service

职责：沿用既有 ECS Service 名称、Tags、Meta 与健康检查字段形状，为现有资源读取链路提供数据。账号为空时使用 `default`；region 固定 `global`；Service 名称为 `third_party_<account>_ecs`，资源 job id 为 `third_party/<account>/ecs/global`。项目保持可选，且不参与 Service 或 job id 命名。

## 5. 关键设计 / 核心机制

### 5.1 批量全量覆盖

请求携带完整集合；服务器验证集合内部与既存机器的冲突后注册 / 更新集合中的 Service，再注销同一第三方归属但不在集合中的 Service。没有单条硬删除接口。

### 5.2 既有 ECS Service 映射

每台机器使用现有 ECS 资源可识别的字段：`ID`、`Name`、`Address`、`Port`、`Tags`、`Meta` 和 TCP `Check`。`Meta` 至少沿用 `iid`、`name`、`region`、`group`、`instance`、`account`、`vendor`、`os`、`cpu`、`mem`、`exp`、`stat`。

账号未提供时规范化为 `default`，region 固定 `global`；项目保留为可选显示信息，不参与命名。

### 5.3 IPv6

验证使用 `net/netip.ParseAddr`。任何 Service `instance` 或 TCP 检查地址用 `net.JoinHostPort(ip, strconv.Itoa(port))` 表示，因此 IPv6 使用方括号；唯一键使用规范化地址加端口。

## 6. 数据模型

### 6.1 提交集合中每台机器

| 字段 | 必填 | 规则 |
|---|---:|---|
| `name` | 是 | 去首尾空白后非空；全局唯一。 |
| `spec` | 是 | 去首尾空白后非空；用于显示的机器规格。 |
| `ip` | 是 | IPv4 或 IPv6，使用规范化地址。 |
| `port` | 是 | JSON 整数，范围 1–65535。 |
| `account` | 否 | 云账号归属；空白时规范化为 `default`。 |
| `project` | 否 | 项目归属；不参与 Service 或 job id 命名。 |

`IP:端口`（IPv6 使用 bracketed host:port）全局唯一。`spec` 原样写入既有 Meta 的 `cpu` 字段，`mem` 固定为“无”；不拆分规格字符串。

### 6.2 Consul

禁止独立 KV、jobs 和 records。Service 必须复用 `internal/sync/writer.go` 所定义的 ECS Service 字段形状；Service 名称为 `third_party_<account>_ecs`、Tags 包含 `ON`、`linux`、`global`，Meta 的 `account` 使用规范化账户、`region` 为 `global`。本文不创建独立的资源模型。

## 7. 校验与错误语义

| 规则 | 行为 |
|---|---|
| 缺少名称、规格、IP 或端口 | 返回现有业务校验失败 envelope。 |
| 无法解析 IPv4 / IPv6 | 返回业务校验失败 envelope。 |
| 端口不是整数或不在 1–65535 | 返回业务校验失败 envelope。 |
| 重复名称或重复规范化 `IP:端口` | 返回业务校验失败 envelope。 |
| 认证缺失、无效或过期 | 保持现有 HTTP 200、`code=50000`、认证异常文案。 |
| Consul 读写失败 | 保持现有 HTTP 200、`code=50000`、服务端日志记录原因。 |

成功继续使用 HTTP 200、`code=20000`。校验失败的具体 `code` / 文案必须以现有写端点实现为准；当前端点尚不存在，不能编造。

## 8. 接口 / API 设计

| 方法 | 路径 | 状态 | 契约 |
|---|---|---|---|
| GET | `/api/edit/cloud?vendor=third_party` | 本期读取扩展 | 已鉴权；返回 `{"code":20000,"data":{"hosts":[...]}}`。从所有第三方 ECS Service 重建完整集合，含规范化 `account`（缺省 `default`）及可选 `project`，不得按账户 / 项目过滤。未带该 vendor 参数时保持原有 `cloud_dict` 语义。 |
| POST | `/api/edit/commit` | 本期写入口 | 请求 `{"vendor":"third_party","hosts":[...]}`；`hosts` 是跨账户完整集合，成功返回 `{"code":20000,"data":{"count":N}}`。 |
| POST | `/api/third-party/*` | 禁止 | 已被产品决策明确排除。 |

仅当 `vendor` 为 `third_party` 时启用本期分支；其它 vendor 保持原来的未实现响应，避免隐式启用云同步任务编辑。

## 9. 技术选型

| 层 | 选择 | 理由 |
|---|---|---|
| 后端 | 既有 Go `net/http` | 遵循仓库现有 HTTP 分层。 |
| 存储 | 既有 Consul KV / ECS Service | 产品明确禁止独立持久化模型。 |
| UI | Vue 2 + Element UI | 复用现有编辑页。 |

## 10. 关键权衡

1. 选择既有编辑模型而非独立 CRUD：最小化接口和运维模型变化，但现有写入模型以云同步任务为中心，必须先补足兼容契约。
2. 选择全量覆盖而非单条删除：请求可完整表达目标状态，因此编辑前读取必须返回所有账户 / 项目归属的完整基线，避免只读子集造成跨账户误删。

## 11. 风险与待验证

- [x] `0.1` / `0.2` 产品规则已确认：复用现有模型、`third_party`、全量覆盖、字段必填与可选归属规则；这不是整个 Phase 0 完成状态。
- [x] 事实核验与扩展：`internal/httpapi/handlers_edit.go` 保持 `/api/edit/cloud` 的 GET 语义；`/api/edit/commit` 仅为 `vendor=third_party` 增加已批准的写入分支，其他厂商仍返回未实现 `50000`。
- [x] 事实核验：`flask-consul/views/edit_cloud.py:50-154` 的遗留 `edit/commit` 会创建 / 删除 `ConsulManager/jobs/<vendor>/<account>/{ecs,rds,redis}/...`，不适合无 API 的第三方。
- [x] 事实核验：`internal/httpapi/handlers_resources.go:65-92` 的既有资源读取需要四段 `job_id`，并以 `<vendor>_<account>_ecs` 加 region Tag 查询 Service。
- [x] `POST /api/edit/commit` 的 `vendor=third_party` 分支已获批：`hosts` 为完整集合，成功 data 返回 count；不创建任务或触发同步。
- [x] 空账号规范化为 `default`，region 为 `global`；Service 为 `third_party_<account>_ecs`，job id 为 `third_party/<account>/ecs/global`；project 不参与命名。
- [x] **Phase 0 完成条件**：写入契约及默认映射均已确认，可以实施后端、前端和测试。
