# 阶段一 QA 验收报告

## 结论摘要

**结论：有条件通过（候选提交 `c4555ad`）。**

阶段一 fixture Provider、统一模型、Consul KV/Service、执行记录、鉴权不重试、限流/超时三次重试、部分失败重跑收敛及同 Job 串行均有真实代码和测试证据。所有指定回归、关键 race 检查与同步测试重复 20 次均通过。

通过率：7/9 通过，1/9 未覆盖，1/9 失败。阻塞上线项：PRD 的 5xx / spec 的临时错误重试未实现。交付风险：本 QA 分支 `b02910a` 不含实现，候选仅在 `c4555ad`。

## 验收标准对照

| 标准 | 状态 | 证据 |
| --- | --- | --- |
| PRD-001 / TASK-001 fixture 到统一模型 | 通过 | `internal/cloud/alicloud/fixture.go:18-66` 解码四类资源，缺 fixture/error 均失败；模型见 `internal/cloud/model/model.go:5-50`。 |
| PRD-002 / TASK-002 KV、Service 写回 | 通过 | group KV：`internal/sync/sync.go:36-49`；对账与注册：`:179-199`、ECS `:227-261`、RDS `:263-291`、Redis `:293-318`；单测使用内存 Consul。 |
| PRD-003 / TASK-003 调度记录闭环 | 有条件通过 | Runtime 分派：`internal/jobs/runtime.go:21-46`；记录：`internal/sync/sync.go:129-153`；job 锁：`internal/scheduler/scheduler.go:138-162`。缺少将 Scheduler → Runtime → fixture → 四类记录串接的单一集成用例。 |
| 鉴权不重试 | 通过 | `retry` 立即退出不可重试错误（`sync.go:156-176`）；`sync_test.go:81-101` 断言 calls=1、record=50000。 |
| 限流/超时最多三次 | 通过 | 最大三次与 10/20ms 回退：`sync.go:156-173`；限流三次 `sync_test.go:117-144`，超时 `:146-173`。 |
| 5xx/临时错误可重试 | 失败（P1） | PRD `PRD.md:12`、spec `spec.md:12` 要求；`sync.go:175-177` 只重试限流/deadline；`fixture.go:38-48` 的其他错误首轮失败。 |
| 部分失败后收敛 | 通过 | 对账 `sync.go:182-199`、失败记录 `:136-153`；`sync_test.go:175-204` 注入注册失败后重跑，Service=1。 |
| 同 Job 串行 | 通过 | `runMu`：`scheduler.go:31-38,150-161`；并发实测 maxActive=1：`scheduler_test.go:58-93`。 |
| 重复执行幂等 | 通过 | `reconcile`：`sync.go:182-199`；二次同步后一个 ECS Service：`sync_test.go:39-79`。 |

## 缺陷明细与整改清单

### P1：5xx / 临时 Provider 错误不符合重试契约

- 责任侧：backend / 接口契约。
- 复现：让 Provider 返回普通临时或 5xx 错误（fixture `error` 不是 `auth`/`rate_limit`/`timeout`），调用任一同步入口。
- 期望：最多调用三次，10ms/20ms 后退；最终成功或写失败 record。
- 实际：`retryable` 为 false，首次即失败。
- 整改：定义可包装的临时/HTTP 5xx 错误；在 `retryable` 识别它；新增“三次 5xx”及“前二次 5xx、第三次成功”断言并覆盖 fixture 映射。

### P1：候选未合入当前验收分支

- 责任侧：交付/集成。
- 证据：QA HEAD=`b02910a`；`git diff --name-status b02910a..c4555ad` 显示阶段一文件只在候选快照。
- 整改：将候选以可追踪提交合入目标分支，并从目标 HEAD 重跑本报告全部命令。

### P2：SPEC-001-01 的断言不完整

- 位置：`spec.md:23`；当前 `internal/sync/sync_test.go:60-78` 仅断言 ECS 部分 Meta/Port 和 ECS record。
- 整改：补 group/RDS/Redis 的 ID、Name、Tags、Meta、`Check.TCP`、`Check.Interval` 与 record 断言；再补 Consul job KV 经 Scheduler/Runtime 的端到端测试。

## 实际命令与结果

- `go test ./...`：PASS。
- `go vet ./...`：PASS（无输出，exit 0）。
- `go build ./cmd/tensuns ./cmd/consulmock`：PASS（无输出，exit 0）。
- `./scripts/smoke.sh`：PASS：`login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config`。
- `go test -race ./internal/scheduler ./internal/jobs ./internal/sync`：PASS。
- `go test ./internal/sync -run '<关键同步测试>' -count=20`：PASS。
- `go test -cover ./internal/sync ./internal/jobs ./internal/scheduler`：PASS，84.0% / 50.0% / 70.7%。
- `git diff --check b02910a..c4555ad`：PASS（无输出）。

## 页面检查与复跑

本次仅后端同步，PRD/spec 无新增页面，页面检查不适用。候选（或合入后目标提交）在 Go 1.22+ 上执行：`go test ./... && go vet ./... && go build ./cmd/tensuns ./cmd/consulmock && ./scripts/smoke.sh`；不连接生产 Consul、真实阿里云或 JumpServer。
