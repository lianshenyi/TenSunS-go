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
# 第三方手工主机

“第三方”需求的产品决策已确认：复用既有 `/api/edit/cloud`、认证、响应 envelope 与 Consul ECS Service 注册模型；机器集合采用全量覆盖，禁止独立 `/api/third-party/*` API、独立 KV 或调度任务。名称、规格、IP、端口必填；账号/项目可选；名称与 `IP:端口` 全局唯一。

第三方写入使用现有 Bearer 鉴权的 `POST /api/edit/commit`：`{"vendor":"third_party","hosts":[...]}`。`hosts` 是跨账户完整集合，成功响应为 `{"code":20000,"data":{"count":N}}`；缺失项会从既有 ECS Service 注册中注销。空账号内部映射为 `default`，region 固定 `global`，因此可通过 `/api/nodes/res?job_id=third_party/default/ecs/global` 读取。该分支绝不创建 `ConsulManager/jobs/*`、同步记录或独立 KV/CRUD。

编辑前必须用现有已鉴权读取入口取得完整基线：`GET /api/edit/cloud?vendor=third_party` 返回 `data.hosts`，包含所有账户和项目归属的第三方主机。把该集合修改后再提交；不要按账户过滤后提交，否则全量覆盖会删除不在请求中的主机。

使用已登录会话返回的 Bearer token（仅在本地受控环境使用）可验证写入；`hosts` 必须是目标完整集合，空数组会删除全部第三方机器：

```bash
curl -sS -X POST http://127.0.0.1:2026/api/edit/commit \
  -H "Authorization: Bearer <TOKEN>" -H 'Content-Type: application/json' \
  --data '{"vendor":"third_party","hosts":[{"name":"manual-v6","spec":"2C4G","ip":"2001:db8::10","port":9100}]}'
curl -sS -H "Authorization: Bearer <TOKEN>" \
  'http://127.0.0.1:2026/api/edit/cloud?vendor=third_party'
curl -sS -H "Authorization: Bearer <TOKEN>" \
  'http://127.0.0.1:2026/api/nodes/res?job_id=third_party%2Fdefault%2Fecs%2Fglobal'
```

Run the focused backend evidence with `go test ./internal/httpapi -run 'TestThirdParty'`; it covers authentication, required fields, IPv4/IPv6 parsing, port bounds, duplicate name / endpoint rejection, default/global mapping, cross-account and project-preserving reads, read-then-save deletion protection, Service replacement, and the no-job invariant.

## Third-party UI verification

Start the API and Vue app with the commands above, log in, then open **云主机列表** and select **编辑云资源**. Choose **第三方** to load the complete cross-account baseline from `GET /api/edit/cloud?vendor=third_party`. Add, edit, or remove rows and select **保存完整集合** to submit the authenticated existing `POST /api/edit/commit` request with `vendor=third_party`.

The form validates names, specifications, IPv4/IPv6 addresses, ports `1–65535`, and duplicate names or `IP:port` before submission. Account and project are optional; an empty account becomes `default` at the API. This is a complete replacement: deleting a row and saving removes that Service, even when it belongs to another account. The UI uses no mock or separate CRUD API.

Use disposable data only, for example `manual-v4` / `2C4G` / `10.0.0.10` / `9100` and `partner-v6` / `4C8G` / `2001:db8::10` / `9100` with account `partner` and project `core`.

```bash
cd vue-consul
npm run lint
npx vue-cli-service test:unit tests/unit/utils/third-party-hosts.spec.js
npm run build:prod
```
