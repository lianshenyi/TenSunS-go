# TASKS-001 多云 fixture 同步

- [x] TASK-001（PRD-001 / SPEC-001）：定义 fixture Provider 与可分类错误。
- [x] TASK-002（PRD-002 / SPEC-001）：实现统一模型到 Consul KV/Service 的写回。
- [x] TASK-003（PRD-003 / SPEC-001）：接入 Runtime、Scheduler 与执行记录。
- [x] TASK-004（PRD-004 / SPEC-001）：增加重试、部分失败、幂等回归测试。
- [x] TASK-005：更新 RUNBOOK 并执行 build、vet、test、smoke。
- [x] TASK-006（PRD-004 / SPEC-001）：补充 5xx/临时错误分类及三次耗尽、重试成功回归测试。
- [x] TASK-007（PRD-001 / SPEC-001）：新增腾讯云、华为云、AWS 的隔离 mock/脱敏 fixture Provider，并注册到 Runtime。
- [x] TASK-008（PRD-002 / SPEC-001）：补齐 group/RDS/Redis 的全字段 Consul 断言，以及新增三云成功、失败、部分写入和幂等回归。
- [x] TASK-009（PRD-003 / SPEC-001）：补齐 Scheduler → Runtime → fixture → 四类 record 的端到端用例。
- [x] TASK-010（PRD-005 / SPEC-002）：实现 JumpServer V2/V3 配置读取、Token 鉴权、节点及资产同步。
- [x] TASK-011（PRD-005 / SPEC-002）：接入 Runtime、手动同步入口和 JMS record，覆盖重试与幂等。
- [x] TASK-012（PRD-006 / SPEC-002）：实现账户级有序 `custom_ecs_info` 覆盖、重复 IP 与空节点清理，并补 V2/V3 收敛和幂等回归。
