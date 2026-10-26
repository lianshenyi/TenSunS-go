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
- `scheduler_disabled=1` (set to `0` only after cloud credentials and read permissions are verified)

Use a local Consul only. Do not put cloud credentials, JumpServer tokens, or production passwords in source control.

## Cloud-account creation and read-only synchronization

`POST /api/jobs` accepts the existing `job_dict.dialogStatus=create` form. The request stores `ak` and `sk` encrypted at `ConsulManager/assets/{vendor}/aksk/{account}`, synchronizes resource groups first, then creates compatible group/ECS/RDS/Redis task KVs.

The production binary registers `alicloud`, `tencent_cloud`, `huaweicloud`, and `awscloud`. AK/SK are decrypted only in process memory for an SDK client and must never be copied into logs, responses, tests, or shell history. No cloud write API is called.

The allowed calls are Alibaba Resource Manager `ListResourceGroups`, ECS `DescribeInstances`, RDS `DescribeDBInstances` (MySQL only), and R-KVStore `DescribeInstances`; Tencent DCDB/CVM/CDB/Redis `Describe*`; Huawei EPS/ECS/RDS/DCS `List*`; and AWS EC2/RDS/ElastiCache `Describe*`. AWS has no resource-group API and uses its legacy fixed group `1`. Alibaba pagination is enabled (ECS/RDS token, Redis page number), and the sync layer converts stored resource-group IDs to display names before writing Consul Services.

Credential or cloud API failures return `50000`; their group failure record is written, but no task KV is created. A resource-job failure writes only its own failure record and does not stop other scheduled jobs. Unit and HTTP flow tests run with:

```bash
go test ./internal/cloud/... ./internal/sync ./internal/accounts ./internal/httpapi ./internal/scheduler
```

For local development keep `scheduler_disabled=1` unless execution adapters are deliberately being tested. Before enabling it in a shared environment, give the cloud account read-only permissions for the listed APIs and submit credentials only through the application UI.

The authenticated “同步” button sends `POST /api/jobs` with `job_dict.dialogStatus=run` and a `jobid`. It runs the scheduled job synchronously and reads the record only afterwards, so the response always describes the run that just finished. Both success and failure return the record's `status` as `code` and `"{update}：{msg}"` as `data`; the record messages are fixed, credential-safe strings written by the sync layer, so they can be returned as-is and never contain provider text. Only when the task has no record or the record cannot be read does the API fall back to the generic `50000` business response `请求处理失败，请查看后端日志！`. Missing jobs and invalid parameters produce the same generic redacted error, and provider errors are logged, never echoed to the client.

The `run` contract is covered by `go test ./internal/httpapi -run TestJobRunContract`, which exercises the success, missing-task, invalid-parameter, provider-failure, and failure-record paths over the authenticated HTTP surface.

## Test data and verification

`make smoke` provisions compatible demonstration data. Log in with the password printed/configured by that setup; no application password is stored in the repository.

```bash
cd vue-consul
npm run build:prod
npm run test:ci
```

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
- If `npm install` fails on an old runtime, use Node 14+ for this Vue CLI 4 project.
