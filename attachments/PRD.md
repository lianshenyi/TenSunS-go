# PRD — Proxmox VE 虚拟机 IP 同步

## 1. 背景与目标

### 1.1 背景

TenSunS-go 需要发现 Proxmox VE（PVE）中的 QEMU 虚拟机，通过 QEMU Guest Agent 获取可连接 IPv4，并同步到 Consul 与 JumpServer。部署环境可能位于内网，PVE 仅使用自签证书或企业私有 CA，不能假设存在公网 CA 签发的证书。

### 1.2 目标

使用专用只读 API Token 接入 PVE 8/9，稳定获取虚拟机 IP 并同步到 Consul 与 JumpServer；TLS **默认严格校验证书和主机名**，同时允许管理员为无法建立受信任证书链的内网 PVE 账户**显式关闭证书校验**。

### 1.3 非目标 (Out of Scope)

- 不使用 PVE 账号密码作为长期服务端凭据。
- 不发现 LXC 容器，不执行 Guest Agent 命令，不控制虚拟机生命周期。
- 不自动下载、信任或替换 PVE 证书。
- 首期不选择 IPv6 作为 Consul 或 JumpServer 连接地址。

## 2. 目标用户

| 角色 | 场景 | 痛点 |
|---|---|---|
| 平台管理员 | 将内网 PVE 虚拟机纳入 Consul 与 JumpServer | PVE 使用自签或私有 CA，默认系统信任库无法验证 |
| 安全管理员 | 管理 PVE 接入凭据与 TLS 风险 | 需要默认安全、最小权限和可审计的例外开关 |

## 3. 核心功能 (MVP)

### F1. PVE 账户接入

- `REQ-PVE-001`：使用完整 HTTPS PVE API 基地址及专用只读 privsep API Token 接入。
- `REQ-PVE-002`：Token Secret 按项目现有 AES 机制加密存储，接口响应和日志不得回显明文。

### F2. 虚拟机与 IP 发现

- `REQ-PVE-003`：发现 QEMU VM，并通过 QEMU Guest Agent 获取地址。
- `REQ-PVE-004`：仅选择非 loopback、非 link-local 的 IPv4，按 CIDR、NIC 和稳定字典序确定优先级；支持静态 IP/端口覆盖。
- `REQ-PVE-005`：虚拟机关机、Guest Agent 不可用、超时或没有合格 IP 时记录 `ip_unavailable`，不使整次同步失败。

### F3. TLS 校验策略

- `REQ-PVE-006`：每个 PVE 账户可配置是否校验证书；默认值必须为开启。只有管理员显式关闭时才跳过证书链与主机名校验。
- 无论是否校验证书，API 地址都必须使用 HTTPS；关闭校验不允许退化为明文 HTTP。
- 关闭校验时，系统应输出不含 Token、Secret 或完整认证头的安全告警，并在配置读取与运行时保持该状态可识别。
- 推荐优先把私有 CA 加入受信任证书链；关闭校验仅作为内网例外选项。

### F4. 下游同步

- `REQ-PVE-007`：将有效 PVE 虚拟机同步到 Consul，并清理失效服务。
- `REQ-PVE-008`：将已注册的 PVE ECS 资产同步到 JumpServer V2/V3；载荷遵循现有 `ecs_info`、`protocols` 和 `accounts` 契约。

## 4. 非功能需求

| 维度 | 要求 |
|---|---|
| 安全 | TLS 校验默认开启；跳过校验须显式配置；Token 最小权限、加密存储且不出现在日志中 |
| 兼容 | 首期支持 PVE 8/9、JumpServer V2/V3 |
| 可靠性 | 单台 VM 无 IP 不影响同账户其他 VM 同步；输出确定性的 IP 选择结果 |
| 可测试性 | 使用 TLS Mock 覆盖默认证书拒绝、受信任证书成功、显式关闭后成功和 HTTP 拒绝 |

## 5. 用户体验流程

1. 管理员配置 HTTPS API 地址、Token ID、加密 Token Secret 和集群名。
2. 系统默认校验证书；使用私有 CA 时，管理员优先把 CA 配入运行环境信任链。
3. 若暂时无法建立信任链，管理员显式关闭该账户的证书校验并获知安全告警。
4. 同步任务发现 QEMU VM、选择 IPv4、写入 Consul，并按配置同步至 JumpServer。
5. 系统记录单 VM 的 `ip_unavailable` 与同步结果，不记录敏感凭据。

## 6. 成功指标

- [ ] 默认配置连接不受信任的自签证书时失败，且错误不泄露凭据。
- [ ] 显式关闭证书校验后，可通过 HTTPS 连接同一内网 PVE TLS Mock。
- [ ] 关闭校验后仍拒绝 HTTP API 地址。
- [ ] 受信任私有 CA 场景无需关闭校验即可成功。
- [ ] PVE VM 可同步到 Consul 与 JumpServer V2/V3，全量 Go 测试通过。

## 7. 风险与依赖

| 风险 | 缓解 |
|---|---|
| 关闭证书校验会暴露中间人攻击风险 | 默认开启、账户级显式配置、告警与文档提示；推荐安装私有 CA |
| Guest Agent 缺失或权限不足 | 记录 `ip_unavailable`，不阻断账户同步，并提供部署前检查说明 |
| PVE 8/9 权限名称不同 | 以目标版本 API Viewer 验证最小权限 |
| JumpServer V2/V3 契约差异 | 使用 Mock 合约测试分别覆盖 |
