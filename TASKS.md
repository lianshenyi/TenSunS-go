# 新增云账户资源组同步任务

**关联事项**：VIDE-39；**功能总任务状态**：待验收

## Phase 0：规格

- [x] 确认 A 范围：可注入 fake provider，不接真实云 API 或 SDK。
- [x] 写入根目录 PRD、技术规格和实施任务。

## Phase 1：实现

- [x] 实现资源组 Provider 到 Consul 的同步服务及记录。
- [x] 实现账户创建编排：凭据加密、资源组前置同步、任务持久化与调度注入。
- [x] 将 `/api/jobs` 的 create 分支连接至可选编排器，并在主程序提供空注册表依赖。

## Phase 2：测试与交付

- [x] 使用 fake provider 补资源组单元测试、编排测试与 HTTP 流程测试。
- [x] 运行 `go build ./...`、`go vet ./...`、`go test ./...`。
- [x] 更新 RUNBOOK，说明当前无真实 provider 的运行限制。

## Phase 3：阿里云资源组只读 Provider（方案 B）

- [x] 确认方案 B 范围：仅阿里云 Resource Manager `ListResourceGroups` 只读 API。
- [x] 引入阿里云官方 SDK，读取既有加密凭据并注册 `alicloud` Provider。
- [x] 覆盖成功、凭据/API 失败及失败后无任务落库保护。
- [x] 更新 PRD、规格与 RUNBOOK，说明只读 API 与其他厂商边界。

## Phase 4：多云只读资源执行（方案 C）

- [x] 对照迁移基线与 Flask 实现，更新 PRD、spec、TASKS，明确资源组、ECS、RDS、Redis 的厂商 API 映射、失败规则和不支持项。
- [x] 实现 `sync.Cloud` 的 ECS/RDS/Redis Consul Service 写入、清理和 record 失败保护；保持阿里云资源任务不支持。
- [x] 接入并注册腾讯云、华为云、AWS Provider 的只读 group/ECS/RDS/Redis API；AWS group 固定映射。
- [x] 补凭据脱敏、Provider 单元、同步组合、调度路由与连续失败保护测试。
- [x] 更新 RUNBOOK，完成本地审查、推送 `main`、部署 `10.0.10.236` 并冒烟；本阶段未使用三家非生产真实云账户。

## Phase 5：阿里云资源只读执行（方案 A）

- [x] 确认范围：阿里云 ECS、RDS（MySQL）、Redis 仅使用 Describe/List API。
- [x] 引入阿里云 ECS/RDS/R-KVStore 官方 SDK，补齐区域 endpoint、分页、状态、地址和资源组 ID 映射。
- [x] 覆盖三类 Provider 的映射、分页、取消和错误脱敏；保留 sync/job/HTTP 的失败隔离组合回归。
- [ ] 完成本地全量验证、审查、推送 `main` 并在 `10.0.10.236` 验证三类任务 record 与回滚点。

## Phase 6：VIDE-39 集成交付与缺陷闭环

- [x] **T-039-001（REQ-039-001/002）** 完成腾讯云、华为云、AWS 实际 Provider fixture、分页/错误路径、Provider→内存 Consul 全资源断言与凭据脱敏回归。执行者：Go 后端工程师；状态：已完成。
- [x] **T-039-002（REQ-039-003）** 在 `10.0.10.236` 的 Go 1.22 Linux 环境完成 tidy-diff、test、vet、build，逐项退出码为 0。执行者：Go 后端工程师；状态：已完成。
- [x] **T-039-003（REQ-039-004）** 将实现、smoke 契约修复和第三方流程修复合入远端 `main`，最终发布基线为 `689b05c536b266b15f02d710b0ef1b38385f141c`。执行者：开发执行者；状态：已完成。
- [x] **T-039-004（REQ-039-004）** 在集成环境发布 API/Web，验证三容器状态、直连/代理 smoke、镜像/提交定位、Consul 数据安全和回滚脚本。执行者：构建分发助手；状态：已完成。
- [x] **T-039-005（BUG-039-001）** 修复并验证“新增云资源 → 第三方”跳转、旧请求 `40000` 提示及零第三方任务写入。执行者：React 前端工程师、Go 后端工程师、构建分发助手；状态：已完成。
- [ ] **T-039-006（统一 QA）** 按 PRD 的 REQ-039-001～004 与 BUG-039-001 对完整用户流程和受影响范围集中验收。依赖：T-039-001～005；执行者：QA；状态：待验收。

**Phase 6 完成标志**：统一 QA 通过后，VIDE-39 才能由“待验收”转为“已完成”。

## 残余风险（不阻塞本阶段待验收）

- 腾讯云、华为云、AWS 的非生产真实账户外部验收本阶段经成员确认不纳入范围。
- 生产启用调度器前，仍需以隔离 Consul ACL 和只读云权限验证真实签名、IAM、区域 endpoint、分页、限流/超时及回滚兼容性。
