# TASKS — Proxmox VE 虚拟机 IP 同步

> 状态：待办 → 进行中 → 阻塞 → 待验收 → 已完成。任务按依赖顺序排列，完成状态只依据实现分支的验证证据更新。

---

## Phase 0 — 决策确认

- [x] **T-001**（已完成，P0，执行者：需求方/需求产品助手）确认使用完整 HTTPS API 地址、专用只读 privsep API Token，不长期保存账号密码。
- [x] **T-002**（已完成，P0，执行者：需求方/需求产品助手）确认 IPv4 选择、静态覆盖、`ip_unavailable` 容错及 Consul + JumpServer 范围。
- [x] **T-003**（已完成，P0，执行者：需求方/需求产品助手）确认 `REQ-PVE-006`：TLS 校验默认开启，内网自签/私有 CA 可账户级显式关闭，仍强制 HTTPS。

**Phase 0 完成标志**：关键产品与安全边界均已明确，无需进一步需求确认。

---

## Phase 1 — PVE 客户端与 TLS 策略

- [ ] **T-004**（进行中，P0，建议执行者：Go 后端工程师；依赖：T-003）在账户配置中增加 `tls_verify`，缺省为 `true`；交付物：配置模型、解析和兼容测试；完成条件：缺字段为 true、显式 false 生效、非法类型被拒绝。
- [ ] **T-005**（进行中，P0，建议执行者：Go 后端工程师；依赖：T-004）由 PVE 客户端按账户构造独立 TLS Transport；交付物：客户端实现；完成条件：默认拒绝不受信任证书、显式关闭后通过、HTTP 始终被拒绝且不影响其他账户。
- [ ] **T-006**（进行中，P0，建议执行者：Go 后端工程师；依赖：T-005）添加安全日志；交付物：WARN 日志与测试；完成条件：日志可识别账户/主机和关闭状态，不含 Token ID、Secret、Authorization。

**Phase 1 完成标志**：`REQ-PVE-006` 的四类 TLS Mock 用例全部通过。

---

## Phase 2 — 资源与下游同步

- [ ] **T-007**（待验收，P0，建议执行者：Go 后端工程师；依赖：T-005）复验 PVE inventory、Guest Agent、IP 优先级、静态覆盖、Consul 注册/清理及 `ip_unavailable`。
- [ ] **T-008**（进行中，P0，建议执行者：Go 后端工程师；依赖：T-007）修复 `BUG-002`：JumpServer V3 载荷补齐 `protocols`、`accounts` 并读取 `ecs_info`；完成条件：V2/V3 Mock 合约测试通过。

**Phase 2 完成标志**：PVE → Consul → JumpServer V2/V3 的 Mock 全链路通过。

---

## Phase 3 — 文档与验收

- [ ] **T-009**（进行中，P1，建议执行者：Go 后端工程师；依赖：T-006）更新 PVE 接入说明和 RUNBOOK，说明私有 CA 优先、`tls_verify` 默认值、关闭风险及配置示例。
- [ ] **T-010**（待办，P0，建议执行者：QA 测试智能体；依赖：T-006、T-008、T-009）按新需求复验，不再使用“禁止任何关闭证书校验”作为标准；运行定向 TLS、JumpServer 合约、`go test ./...`、`go vet ./...`、构建和 smoke。
- [ ] **T-011**（待办，P1，建议执行者：需求产品助手；依赖：T-010）对照 `REQ-PVE-001` 至 `REQ-PVE-008` 审查证据并给出通过/有条件通过/驳回结论。

**Phase 3 完成标志**：需求、实现、测试与文档一致，独立 QA 通过。

---

## 后续阶段（不在 MVP / 可选增强）

- [ ] 提供 UI 表单管理 PVE Token 与 TLS 策略。
- [ ] 支持上传/选择 PVE 私有 CA，而无需关闭证书校验。
- [ ] 支持 IPv6 连接地址与 LXC 发现。

---

## 进度跟踪

| Phase | 状态 | 完成日期 |
|---|---|---|
| 0 决策 | 已完成 | 2026-10-31 |
| 1 客户端与 TLS | 进行中 | |
| 2 资源与下游 | 进行中 | |
| 3 文档与验收 | 待办 | |

---

## 速查命令

```bash
go test ./internal/pve ./internal/jumpserver ./internal/sync
go test ./...
go vet ./...
go build ./cmd/tensuns ./cmd/consulmock
make smoke
```
