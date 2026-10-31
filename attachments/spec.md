# spec — Proxmox VE 虚拟机 IP 同步技术设计

**产品形态**：TenSunS-go 后端中的 PVE 只读发现与同步适配器  
**状态**：设计稿 v0.2（2026-10-31）；已按需求方意见修订 TLS 校验策略

---

## 1. 概述

- `vendor=pve`，展示名 `Proxmox VE`；`region` 表示 PVE 集群，节点写入资源属性。
- 使用 HTTPS API 基地址与 PVE API Token，仅读取 QEMU inventory 和 Guest Agent 网络接口。
- 选取可连接 IPv4，写入 Consul ECS，并同步至 JumpServer V2/V3。
- TLS 默认严格校验，允许账户级显式关闭证书校验以适配内网自签/私有 CA。

## 2. 目标 / 非目标

### Goals

- 满足 `REQ-PVE-001` 至 `REQ-PVE-008`。
- 保持现有 AES、Consul 任务 `func/args/id` 与 JumpServer 资产契约兼容。
- 对跳过证书校验的例外配置提供明确、可测试、可审计的安全边界。

### Non-Goals（明确不做）

- 不支持明文 HTTP、账号密码登录、LXC 或 IPv6 连接地址。
- 不在本期实现私有 CA 文件上传或证书生命周期管理。
- 不调用 Guest Agent exec、控制台或虚拟机控制类 API。

## 3. 架构总览

任务调度器读取 Consul 中的 PVE 账户配置，解密 Token Secret，并创建账户专属 PVE HTTP 客户端。客户端读取集群 QEMU inventory，再逐台读取 Guest Agent 网络接口。同步层应用 IP 选择与静态覆盖规则，将可用资源注册到 Consul；JumpServer 同步器随后读取 Consul ECS 服务，按 V2/V3 契约创建或更新资产。

## 4. 系统组件 / 模块

### 4.1 PVE 客户端

职责：

- 校验 API 地址、认证配置与 TLS 策略。
- 发送只读 API 请求，解析 QEMU VM 与 Guest Agent 地址。

### 4.2 PVE 同步器

职责：

- 应用 IP 优先级、静态覆盖和 `ip_unavailable` 语义。
- 注册或清理 Consul 服务，写入任务执行记录。

### 4.3 JumpServer 同步器

职责：

- 读取 PVE ECS 服务和既有 `ecs_info`。
- 分别生成符合 JumpServer V2/V3 的资产、协议与账号载荷。

## 5. 关键设计 / 核心机制

### 5.1 TLS 策略

账户配置新增布尔字段 `tls_verify`，序列化时省略或未提供均解释为 `true`。仅当值被明确设置为 `false` 时，PVE 客户端为该账户构造独立的 `http.Transport`，设置 `TLSClientConfig.InsecureSkipVerify=true`。不得修改 `http.DefaultTransport` 或共享其他账户的 Transport。

安全不变量：

1. `api_base_url` 始终必须是绝对 HTTPS URL。
2. `tls_verify=false` 只跳过证书链与主机名校验，不改变 Token、超时、只读 API 范围等约束。
3. 创建跳过校验的客户端时记录一次 WARN，字段只含账户/集群标识和 API 主机，不得包含 Token ID、Token Secret、Authorization 或 URL 用户信息。
4. 默认路径必须使用 Go 标准证书校验；私有 CA 已加入系统/容器信任库时无需关闭校验。

不采用“接受任意外部 HTTP client 后再检查其 Transport”的旧验收方向。TLS Transport 应由 PVE 客户端根据结构化配置自行构造，避免调用方绕过默认策略或共享可变 Transport。

### 5.2 IP 选择

仅保留有效 IPv4，排除 loopback、link-local 和 unspecified。依次按 `cidr_priority`、`nic_priority`、IP/接口名字典序选择。Guest Agent 失败返回空 IP，由同步层记录 `ip_unavailable`。

### 5.3 JumpServer 契约

V2/V3 都必须从现有配置读取资产连接信息。V3 请求载荷必须包含 `protocols` 和 `accounts`；Mock 测试应验证字段内容，而非只验证 HTTP 状态码。

## 6. 数据模型

Consul PVE 账户配置：

```json
{
  "api_base_url": "https://pve.internal:8006/api2/json",
  "token_id": "reader@pve!tensuns",
  "token_secret": "<AES encrypted>",
  "cluster": "lab-cluster",
  "tls_verify": true,
  "ip_selection": {
    "nic_priority": ["ens18"],
    "cidr_priority": ["10.0.0.0/8"]
  }
}
```

兼容规则：已有账户没有 `tls_verify` 字段时按 `true` 处理。只有 JSON 布尔值 `false` 表示关闭；不接受含义模糊的字符串值。

## 7. 接口 / API 设计

PVE 读取端点：

| 方法 | 端点 | 用途 |
|---|---|---|
| GET | `/cluster/resources?type=vm` | 读取 QEMU VM inventory |
| GET | `/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces` | 读取 Guest Agent 网络接口 |

认证头：`Authorization: PVEAPIToken=<token-id>=<secret>`。日志禁止输出该头。

## 8. 技术选型

| 层 | 选择 | 理由 |
|---|---|---|
| 后端 | Go 标准库 `net/http`、`crypto/tls` | 保持仓库无第三方 Go 依赖 |
| 存储 | Consul KV / Service | 沿用现有项目契约 |
| UI | 首期无专用表单 | 通过加密 Consul KV 配置，避免伪装成 AK/SK |
| 部署 | 现有 Docker / Compose | 沿用当前部署方式与系统证书信任库 |

## 9. 关键权衡

1. 私有 CA 信任优于跳过校验，但内网落地需要例外开关；因此选择“默认开启 + 显式账户级关闭 + 告警”，代价是关闭时无法抵御中间人攻击。
2. TLS Transport 由 PVE 客户端构造，减少任意注入带来的策略绕过；代价是测试需通过 CA/配置选项控制，而不是传入任意 client。

## 10. 里程碑（分阶段路线图）

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| M0 契约 | PVE 配置、Token、TLS 与 IP 规则 | 配置解析和 TLS Mock 测试通过 |
| M1 同步 | PVE → Consul | VM 注册、覆盖、清理和容错测试通过 |
| M2 下游 | Consul → JumpServer V2/V3 | V2/V3 Mock 合约测试通过 |
| M3 验收 | 文档、全量测试与安全复核 | `go test ./...`、`go vet ./...`、构建与冒烟通过 |

## 11. 风险与待验证

- [ ] 在目标 PVE 8/9 环境用 API Viewer 验证最小权限。
- [ ] 验证容器内安装私有 CA 后，`tls_verify=true` 可成功连接。
- [ ] 验证 `tls_verify=false` 产生告警且日志不含 Token。
- [ ] 验证 JumpServer V2/V3 真实环境载荷兼容性。
