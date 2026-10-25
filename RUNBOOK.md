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
- `scheduler_disabled=1` (recommended while cloud adapters are unavailable)

Use a local Consul only. Do not put cloud credentials, JumpServer tokens, or production passwords in source control.

## Cloud-account creation (Alibaba Cloud resource-group-only boundary)

`POST /api/jobs` accepts the existing `job_dict.dialogStatus=create` form. The request stores `ak` and `sk` encrypted at `ConsulManager/assets/{vendor}/aksk/{account}`, synchronizes resource groups first, then creates compatible group/ECS/RDS/Redis task KVs.

The production binary registers the `alicloud` Provider only. It uses the official Alibaba Cloud Resource Manager SDK to call read-only `ListResourceGroups` (first page, 100 items) after credentials have been stored as ciphertext. AK/SK are decrypted only in process memory for that call and must never be copied into logs, responses, tests, or shell history. No Alibaba Cloud write API is called.

Other cloud vendors remain unregistered and return business code `50000`. Credential or Alibaba API failures also return `50000`; their group failure record is written, but no task KV is created. Unit and HTTP flow tests run with:

```bash
go test ./internal/cloud/alicloud ./internal/sync ./internal/accounts ./internal/httpapi
```

For local development keep `scheduler_disabled=1` unless execution adapters are deliberately being tested. Resource-group sync is the only cloud operation implemented by this change; ECS, RDS, and Redis task execution remains unavailable.

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
