# 新增云账户资源组同步任务

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
- [x] 更新 RUNBOOK，完成本地审查、推送 `main`、部署 `10.0.10.236` 并冒烟；真实凭据仅经应用流程验证。
