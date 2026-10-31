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

## Test data and verification

`make smoke` provisions compatible demonstration data. Log in with the password printed/configured by that setup; no application password is stored in the repository.

```bash
cd vue-consul
npm run build:prod
npm run test:ci
```

## PVE account form

In **云主机列表 → 新增云资源**, select **Proxmox VE** and provide a HTTPS `api_base_url`, `token_id`, `token_secret`, and PVE cluster name. Certificate verification is enabled by default. Prefer installing a private CA in the API container or system trust store; turn it off only for a controlled internal self-signed-certificate exception. The UI never re-displays a saved Token Secret. To replace PVE credentials during editing, enable **更新 PVE 连接配置** and enter all three credential fields again.

The current Go migration baseline exposes the form but does not yet implement `POST /api/jobs` or `POST /api/edit/commit`; those requests return business code `50000`. Do not enter production PVE credentials until the write endpoints are deployed. No mock credential source is used by the UI.

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
