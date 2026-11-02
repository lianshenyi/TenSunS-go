# spec — 第三方手工录入主机技术设计

**产品形态**：TenSunS-go 的一个受鉴权保护的手工资产 API，供现有 Vue 2 + Element UI 前端使用。
**状态**：设计稿 v1.0（2026-11-02）；✅ 联调契约已锁定，尚未实现服务端或前端。

---

## 1. 概述

- 厂商内部键固定为 `third_party`，页面展示名固定为“第三方”。
- 第三方账户只是手工资产的命名空间，不是云账户，不存在 AK/SK、区域选择或定时同步。
- 一个主机有后端生成的全局 `iid`，并在一个第三方账户内唯一占用一个 IP。
- 所有资源写入 Consul；列表以资产 KV 为权威数据源，Consul Service 用于现有服务生态和健康检查。
- 新接口在 `/api/third-party/` 下，不改变遗留 `/api/edit/*`、`/api/jobs`、`/api/nodes/*` 的契约。

## 2. 目标 / 非目标

### Goals

- 提供账户和主机的单条 CRUD API，以及明确的校验和失败语义。
- 用现有 `Authorization` Bearer Token、`code` 业务码、Consul `store.Store` 接口实现。
- 写出的 Service Meta 与现有 ECS 资源字段兼容，方便未来扩展消费方。

### Non-Goals（明确不做）

- 不使用 Gin/GORM，不引入第三方依赖；当前仓库使用标准库 `net/http` 和 Consul。
- 不创建调度任务、任务记录或资源组 KV，不向云厂商拉取数据。
- 不支持批量操作和 JumpServer 同步。
- 不把第三方账户加入 `GET /api/edit/cloud`；该接口只表达有 `group` 任务的云账户。

## 3. 架构总览

浏览器携带登录返回的 Bearer Token 调用 Go API。HTTP handler 完成 JSON 解码、字段校验和业务错误映射；第三方资产服务读写 Consul KV，并注册或撤销对应 Consul Service。读取列表只读取规范 KV，以避免健康检查状态影响资产可见性。前端因此不调用任何云适配器或调度器。

## 4. 系统组件 / 模块

### 4.1 Vue 第三方资产页面

职责：

- 维护账户选择、新增/编辑主机表单、删除确认和后端校验错误展示。
- 经 `vue-consul/src/utils/request-ops.js` 自动附带 Authorization，并仅调用第 7 节 API。

### 4.2 `internal/httpapi` 路由与 handler

职责：

- 将 `/api/third-party/` 置于已有 `withAuth` 中间件之后。
- 调用第三方资产业务逻辑；不将客户端请求直接拼接成 Consul 路径。

### 4.3 第三方资产服务

职责：

- 生成 `iid`、规范化字段、检查账户与 IP 唯一性。
- 读写第 6 节 KV，并构造兼容的 `store.ServiceRegistration`。

### 4.4 Consul

职责：

- 保存账户和资产的 JSON 文档。
- 通过 `agent/service/register` 保存可发现的主机，删除时 `agent/service/deregister/{iid}`。

## 5. 关键设计 / 核心机制

### 5.1 独立资产模型，不伪造云任务

`third_party` 不代表云适配器。若写入 `ConsulManager/jobs/third_party/...`，启动加载的调度器会尝试执行未注册厂商；因此第三方记录不写 jobs/record/group 路径。未来若需任务列表或 JumpServer 同步，必须另立兼容设计。

### 5.2 两份 Consul 表示的写入顺序

主机规范文档是权威数据。创建：先检查冲突，再写资产 KV，注册 Service；更新：更新 KV 后重注册 Service；删除：先注销 Service，后删除 KV。任一步失败都返回 `50000`，必须记录 `iid`、操作和底层错误，绝不返回 `20000`。实现应对失败作尽力补偿，并由测试覆盖 Service 注册失败的返回路径。

### 5.3 编辑和删除语义

- `PATCH` 是完整替换，不是部分合并：请求必须携带创建时所有必填字段；省略可选字段会恢复默认值。
- `iid` 仅在 URL 给出，正文的 `iid` 一律忽略/拒绝，防止跨主机覆盖。
- 删除是硬删除：注销服务并删除资产 KV，后续 GET 返回“不存在”。
- 删除账户仅允许没有主机；有主机时返回冲突，永不级联删除。

## 6. 数据模型

### 6.1 Consul KV

| 路径 | JSON | 说明 |
|---|---|---|
| `ConsulManager/assets/third_party/accounts/{account}` | `{"account":"edge","created_at":"..."}` | 账户命名空间。 |
| `ConsulManager/assets/third_party/hosts/{iid}` | 下方 `Host` | 主机权威记录。 |

`account` 不得直接来自未校验的路径片段；实现仅接受正则 `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`。

```json
{
  "iid": "third-550e8400-e29b-41d4-a716-446655440000",
  "account": "edge",
  "name": "partner-web-01",
  "ip": "2001:db8::10",
  "port": 9100,
  "cpu": "4核",
  "mem": "8GB",
  "os": "linux",
  "group": "手工录入",
  "status": "RUNNING",
  "created_at": "2026-11-02T16:00:00Z",
  "updated_at": "2026-11-02T16:00:00Z"
}
```

`iid` 由后端用 `crypto/rand` 生成 RFC 4122 v4 UUID 并加 `third-` 前缀；客户端不可传入或选择它。

### 6.2 Consul Service 映射

每个主机注册：

```json
{
  "ID": "third-...",
  "Name": "third_party_edge_ecs",
  "Address": "2001:db8::10",
  "Port": 9100,
  "Tags": ["ON", "linux", "manual"],
  "Meta": {
    "iid": "third-...",
    "name": "partner-web-01",
    "region": "手工录入",
    "group": "手工录入",
    "instance": "[2001:db8::10]:9100",
    "account": "edge",
    "vendor": "第三方",
    "os": "linux",
    "cpu": "4核",
    "mem": "8GB",
    "exp": "无",
    "stat": "on"
  },
  "Check": {"TCP":"[2001:db8::10]:9100","Interval":"60s"}
}
```

IPv4 `instance`/`TCP` 是 `ip:port`；IPv6 必须按 `net.JoinHostPort(ip, strconv.Itoa(port))` 加方括号。`status=STOPPED` 时 Tags 第一项为 `OFF`、`Meta.stat=off`；其他字段不变。

## 7. 接口 / API 设计

### 7.1 通用契约

- 除登录外所有下列端点必须有 `Authorization: Bearer <HS512 token>`。缺失、错误或过期时，HTTP 200，`{"code":50000,"data":"403：认证异常，请重新登录！"}`。
- 请求为 `Content-Type: application/json`；上限沿用服务端 1 MiB JSON body 限制。
- 业务成功始终 HTTP 200 + `code:20000`。校验失败、重复、找不到资源、非空账户删除均 HTTP 200 + `code:40000`，`data` 为可直接展示的中文错误字符串。存储或 Consul 失败为 HTTP 200 + `code:50000`。
- 路径不存在沿用 HTTP 404 + `code:50000`；不支持的方法沿用 HTTP 405 + `code:50000`。
- 所有时间为 RFC 3339 UTC 字符串。列表按 `created_at` 升序、`iid` 升序稳定排序。

### 7.2 字段与校验

| 字段 | 创建/编辑 | 校验与默认值 |
|---|---|---|
| `account` | 创建账户、创建主机必填；编辑取 URL | 1–64 字符，匹配账户正则。 |
| `name` | 必填 | 去空格后 1–128 Unicode 字符。 |
| `ip` | 必填 | 去空格后由 `net/netip.ParseAddr` 解析；拒绝 unspecified、multicast。每账户唯一。 |
| `port` | 必填 | JSON 整数 1–65535，不接受字符串或小数。 |
| `cpu` | 必填 | 去空格后 1–64 Unicode 字符。 |
| `mem` | 必填 | 去空格后 1–64 Unicode 字符。 |
| `os` | 可选 | 省略或空字符串默认为 `linux`；仅 `linux`、`windows`。 |
| `group` | 可选 | 省略或空字符串默认为 `手工录入`；最长 128 字符。 |
| `status` | 可选 | 省略或空字符串默认为 `RUNNING`；仅 `RUNNING`、`STOPPED`。 |

未知 JSON 字段必须拒绝为 `40000`，避免客户端拼写错误被静默吞掉。`account` 中不允许 `/`、`.` 或空白；`name`/`group`/规格字段不得保留前后空白。

### 7.3 端点

| 方法 | 路径 | 请求 | 成功响应 `data` | 语义 |
|---|---|---|---|---|
| GET | `/api/third-party/accounts` | — | `[{"account":"edge","created_at":"..."}]` | 返回所有账户。 |
| POST | `/api/third-party/accounts` | `{"account":"edge"}` | `{"account":"edge","created_at":"..."}` | 新建账户；存在则 `40000`。 |
| DELETE | `/api/third-party/accounts/{account}` | — | `"第三方账户删除成功！"` | 仅空账户可删除；不存在或非空均 `40000`。 |
| GET | `/api/third-party/hosts?account=edge` | `account` 必填 | `[{Host}]` | 返回该账户主机。 |
| POST | `/api/third-party/hosts` | 下方创建示例 | `{Host}` | 账户必须存在，生成 iid 并注册 Service。 |
| GET | `/api/third-party/hosts/{iid}` | — | `{Host}` | 读取单台主机。 |
| PATCH | `/api/third-party/hosts/{iid}` | 下方编辑示例 | `{Host}` | 完整替换可编辑字段，保持 iid/account/created_at。 |
| DELETE | `/api/third-party/hosts/{iid}` | — | `"第三方主机删除成功！"` | 注销 Service 并硬删除资产 KV。 |

创建请求（`iid`、时间均不得出现）：

```json
{"account":"edge","name":"partner-web-01","ip":"10.20.30.40","port":9100,"cpu":"4核","mem":"8GB","os":"linux","group":"合作方","status":"RUNNING"}
```

编辑请求（`account` 不出现，所有必填的可编辑字段均出现）：

```json
{"name":"partner-web-01","ip":"10.20.30.41","port":9200,"cpu":"4核","mem":"8GB","os":"linux","group":"合作方","status":"RUNNING"}
```

成功 envelope 示例：

```json
{"code":20000,"data":{"iid":"third-...","account":"edge","name":"partner-web-01","ip":"10.20.30.40","port":9100,"cpu":"4核","mem":"8GB","os":"linux","group":"合作方","status":"RUNNING","created_at":"2026-11-02T16:00:00Z","updated_at":"2026-11-02T16:00:00Z"}}
```

失败示例：

```json
{"code":40000,"data":"字段 ip：必须是可路由的 IPv4 或 IPv6 地址！"}
```

## 8. 技术选型

| 层 | 选择 | 理由 |
|---|---|---|
| 后端 | Go 1.22、`net/http` | 与现有 `internal/httpapi` 一致，不扩大技术栈。 |
| 存储 | Consul KV + Agent Service | 复用现有 `store.Store` 与资源发现方式。 |
| UI | Vue 2 + Element UI | 与 `vue-consul/` 的现有实现一致。 |
| 部署 | 既有 Docker Compose | 无新增运行时依赖。 |

## 9. 关键权衡

1. 选择独立 `/api/third-party/*` 而非复用 `/api/edit/commit`：后者包含云凭据和调度任务语义，不适合无 API 的资产。
2. 选择 KV 作为列表权威来源、Service 为投影：Service 可能被健康检查影响或被外部管理，KV 可完整表达可编辑资产。
3. 选择“账户必须为空才删除”而不级联：避免一次误操作删除多个可发现 Service。

## 10. 里程碑（分阶段路线图）

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| M0 契约 | 三层文档与前后端接口锁定 | 本文第 7 节可供前端 mock/实现。 |
| M1 后端 | 数据模型、CRUD、Consul 读写、测试 | httptest 验证 CRUD、认证、校验、KV/Service 副作用。 |
| M2 前端 | Vue 2 页面、表单与错误态 | 可以完成账户和单机 CRUD。 |
| M3 联调 | Compose 环境真实 Consul 联调 | 前后端构建与既有回归通过。 |

## 11. 风险与待验证

- [x] 契约不写入 scheduler 的 jobs/record 路径。
- [x] 认证、响应码、IPv4/IPv6、唯一性和删除语义已明确。
- [ ] 后端实现时验证 KV 与 Service 任一写失败的补偿与日志。
- [ ] 在真实 Consul 验证 IPv6 TCP health check 的地址格式。
- [ ] 产品验收前决定是否把第三方主机纳入 JumpServer 同步。
