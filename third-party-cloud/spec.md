# spec — 第三方云厂商手工机器录入技术设计

**产品形态**：TenSunS-go 既有云资源编辑模型中的 `third_party` 厂商扩展。
**状态**：设计稿 v1.2（2026-11-03）；产品决策已确认。经代码核验，现有写入能力尚不能承载该契约；在不新建模型的约束下暂不可实现。

---

## 1. 概述

- vendor 内部键固定为 `third_party`，展示名为“第三方”。
- 复用现有 `/api/edit/cloud`、Bearer 鉴权、响应 envelope、Consul KV 与 ECS Service 模型；不增加独立 CRUD 或 KV 前缀。
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

浏览器沿用现有 Vue 2 编辑页和 Bearer Token，读取仍经 `GET /api/edit/cloud`。完整集合必须经既有编辑写入契约保存；后端以当前 ECS Service 注册模型写入，并将缺失的旧 Service 注销。现有 `/api/nodes/res` 通过 Service 名称和 Tags 读取这些资源。

**阻塞点**：当前代码没有满足该模型的写入契约；详见 §11。因此本节描述的是已确认目标架构，不是可调用的新接口。产品决策已解除，剩余的是既有实现与目标契约的事实差异，不能由实现方自行补全。

## 4. 系统组件 / 模块

### 4.1 Vue 2 + Element UI 编辑页

职责：增加 `third_party` 选项、维护可增删的机器行，并在提交前执行 §7 校验；不引入独立页面或 API client。

### 4.2 `internal/httpapi` 编辑 handler

职责：保持 `/api/edit/cloud` 的读语义和鉴权；在确认的既有写端点上解析完整机器集合、校验并交由 Consul Service 保存。

### 4.3 Consul ECS Service

职责：沿用 `<vendor>_<account>_ecs` Service 名称及 ECS Meta 字段，为 `/api/nodes/res` 提供资源列表数据。全量覆盖时注销本次集合缺失的同归属 Service。

## 5. 关键设计 / 核心机制

### 5.1 批量全量覆盖

请求携带完整集合；服务器验证集合内部与既存机器的冲突后注册 / 更新集合中的 Service，再注销同一第三方归属但不在集合中的 Service。没有单条硬删除接口。

### 5.2 既有 ECS Service 映射

每台机器使用现有 ECS 资源可识别的字段：`ID`、`Name`、`Address`、`Port`、`Tags`、`Meta` 和 TCP `Check`。`Meta` 至少沿用 `iid`、`name`、`region`、`group`、`instance`、`account`、`vendor`、`os`、`cpu`、`mem`、`exp`、`stat`。

没有提供账号时，Service 归属、名称及用于 `resourceMetas` 的 job id 尚无法从既有契约安全推导；这是 §11 的确认项之一。

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
| `account` | 否 | 云账号归属；缺省语义待确认。 |
| `project` | 否 | 项目归属；缺省语义待确认。 |

`IP:端口`（IPv6 使用 bracketed host:port）全局唯一。字段 `spec` 与现有 ECS Meta 的 `cpu` / `mem` 映射尚未由产品确认，不能擅自拆分或改名。

### 6.2 Consul

禁止独立 KV。最终具体写入键必须是项目已存在、且不会使调度器运行第三方适配器的既有模型。Service 必须复用 `internal/sync/writer.go` 所定义的 ECS Service 字段形状；最终 Service Name、Tags 中 region 和可选 account / project 的空值表达须在 §11 确认后锁定。

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
| GET | `/api/edit/cloud` | 已存在 | 已鉴权；返回 `{"code":20000,"cloud_dict":...}`，仅由 jobs 推导账户。 |
| POST | `/api/edit/commit` | 占位 | 当前返回 `50000`；不能作为已发布第三方保存接口。 |
| POST | `/api/third-party/*` | 禁止 | 已被产品决策明确排除。 |

**待确认的最小兼容写入契约**：产品需指定是在 `/api/edit/commit` 增加何种 `editJob` 分支，或提供哪个已有非独立写入口；并明确完整机器集合的 JSON 容器字段、成功 `data` 字段、可选 account/project 缺省归属和 Service 的 `job_id` / region 表示。在确认前不得实现或让前端联调写请求。

## 9. 技术选型

| 层 | 选择 | 理由 |
|---|---|---|
| 后端 | 既有 Go `net/http` | 遵循仓库现有 HTTP 分层。 |
| 存储 | 既有 Consul KV / ECS Service | 产品明确禁止独立持久化模型。 |
| UI | Vue 2 + Element UI | 复用现有编辑页。 |

## 10. 关键权衡

1. 选择既有编辑模型而非独立 CRUD：最小化接口和运维模型变化，但现有写入模型以云同步任务为中心，必须先补足兼容契约。
2. 选择全量覆盖而非单条删除：请求可完整表达目标状态，但必须在保存前可靠识别同一第三方归属的旧 Service。

## 11. 风险与待验证

- [x] 产品已确认：复用现有模型、`third_party`、全量覆盖、字段必填与可选归属规则。
- [x] 事实核验：`internal/httpapi/handlers_edit.go:10-29` 中 `/api/edit/cloud` 只处理 GET，`/api/edit/commit` 返回未实现 `50000`。
- [x] 事实核验：`flask-consul/views/edit_cloud.py:50-154` 的遗留 `edit/commit` 会创建 / 删除 `ConsulManager/jobs/<vendor>/<account>/{ecs,rds,redis}/...`，不适合无 API 的第三方。
- [x] 事实核验：`internal/httpapi/handlers_resources.go:65-92` 的既有资源读取需要四段 `job_id`，并以 `<vendor>_<account>_ecs` 加 region Tag 查询 Service。
- [ ] 实现授权 / 契约补充：在既有 `POST /api/edit/commit` 中明确第三方完整集合的 JSON 容器和成功响应字段；当前 Go handler 固定返回未实现，Python 同名实现只接受云同步任务 `editJob`。
- [ ] 实现授权 / 契约补充：明确 account/project 省略时的稳定归属值，以及 Service Name、region Tag、`job_id` 的映射；不能以默认值替代，因为这会改变已确认的可选语义。
- [ ] 上述差异消除后，才可落实后端、前端和测试实现。
