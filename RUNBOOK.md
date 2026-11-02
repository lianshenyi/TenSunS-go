# TenSunS-go local runbook

## Services and ports

- Go API: `127.0.0.1:2026`
- Vue development server: `127.0.0.1:1026` (or the port reported by Vue CLI)
- Consul: `127.0.0.1:8500` (use the in-memory mock for smoke testing)

## Start locally

```bash
make build
./bin/consulmock -addr 127.0.0.1:8500
CONSUL_ADDR=127.0.0.1:8500 ./bin/tensuns
cd vue-consul && npm install && npm run dev
```

Run `make smoke` from the repository root for the integrated compatibility check; it starts and stops its own mock Consul and API processes.

## Environment variables

- `CONSUL_ADDR=127.0.0.1:8500`
- `TENSUNS_API=http://127.0.0.1:2026` (optional Vue dev proxy override)
- `scheduler_disabled=1` (use while validating or before configuring a cloud account)

AWS account credentials are stored only in Consul at
`ConsulManager/assets/awscloud/aksk/<account>` in the existing AES-encrypted
`ak`/`sk` shape. The process decrypts them only while creating the AWS SDK
client; do not place plaintext cloud credentials in environment variables.

Tencent Cloud and Huawei Cloud use the same AES-encrypted `ak`/`sk` shape at
`ConsulManager/assets/tencent_cloud/aksk/<account>` and
`ConsulManager/assets/huaweicloud/aksk/<account>`. Grant each account
read-only CVM/CDB/Redis/DCDB or ECS/RDS/DCS/EPS/BSS permissions before enabling
its jobs. Tencent resource-group discovery calls DCDB in `ap-guangzhou`;
Huawei enterprise projects and ECS expiry dates call EPS in `cn-north-4` and
BSS in `cn-north-1`, respectively.

Use a local Consul only. Do not put cloud credentials, JumpServer tokens, or production passwords in source control.

## Test data and verification

`make smoke` provisions compatible demonstration data. Log in with the password printed/configured by that setup; no application password is stored in the repository.

```bash
cd vue-consul
npm run build:prod
npm run test:ci
```

### Go 1.22 verification plan

The module intentionally declares `go 1.22` and has no `toolchain` directive.
Run the backend gate in a Go 1.22 environment before merging; a local Docker
engine can provide that environment without changing the developer toolchain:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.22 \
  sh -ec 'go mod tidy -go=1.22 && git diff --exit-code -- go.mod go.sum && go test ./... && go vet ./... && go build ./cmd/tensuns'
```

CI should run the identical four Go commands on a pinned Go 1.22 runner. It
must fail if `go mod tidy -go=1.22` changes `go.mod` or `go.sum`; this catches
an accidental SDK upgrade that raises the minimum toolchain version.

## Authenticated identity check

An authorized reviewer can verify the deployed identity contract without placing a password or token in source, shell history, logs, or issue comments:

```bash
python3 scripts/verify-user-info.py http://10.0.10.236:1026
```

The script prompts on the controlling terminal with echo disabled, keeps the password and returned token in process memory only, and prints only PASS/FAIL. The minimum access needed is one valid administrator password for the target environment, supplied directly to the reviewer through an approved out-of-band channel; do not send it in this repository or a Multica comment.

The expected authenticated response is `roles=["admin"]`, `name="admin"`, and `avatar="/harbor-mark.svg"`. `make smoke` checks the same avatar contract against its disposable local fixture.

## Common problems

- If the Vue app cannot call `/api`, start the Go API first and check `TENSUNS_API` / `vue.config.js` proxy settings.
- If API startup fails, confirm Consul is listening at `CONSUL_ADDR` and its KV data has been seeded.
- If an AWS job writes a failure record, check its `msg` in
  `ConsulManager/record/jobs/awscloud/<account>/<type>/<region>` and confirm
  the account has least-privilege permissions for EC2, RDS, and ElastiCache.
- For Tencent or Huawei failures, inspect the equivalent provider record and
  verify the selected region and read-only API permissions (including DCDB,
  EPS, and BSS for project/expiry discovery); credentials are never written to
  application logs.
- If `npm install` fails on an old runtime, use Node 14+ for this Vue CLI 4 project.
# 第三方手工主机：当前状态

“第三方”需求的产品决策已确认：复用既有 `/api/edit/cloud`、认证、响应 envelope 与 Consul ECS Service 模型；机器集合采用全量覆盖，禁止独立 `/api/third-party/*` API、独立 KV 或调度任务。

当前仓库**尚未实现**对应写入：`/api/edit/cloud` 仅支持读取，`POST /api/edit/commit` 返回未实现错误；遗留 Flask 的同名写入会创建云同步任务，不能直接用于无 API 的第三方。不要向 `/api/edit/commit`、`/api/jobs` 或 `/api/nodes/cstecs` 发送第三方机器数据，也不要写入 `ConsulManager/jobs/third_party/*`。

继续实现前，必须确认第三方完整机器集合所用的既有写端点、请求/成功响应字段，以及可选 account/project 缺省时的 Service 归属与 `job_id`/region 映射。详见 `third-party-cloud/spec.md` 第 8、11 节；因此目前没有可执行的第三方 curl 联调样例。
