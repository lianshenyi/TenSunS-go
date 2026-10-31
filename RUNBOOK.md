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

Use a local Consul only. Do not put cloud credentials, JumpServer Access Key Secrets, legacy tokens, or production passwords in source control.

## JumpServer authentication

Create a dedicated API Key in the JumpServer Web UI (personal information → API Key), then configure its Access Key ID and the Secret Key in TenSunS. The Secret Key is only entered when adding or explicitly rotating it; it is AES-encrypted in Consul and is never returned by `GET /api/jms/config`. Both JumpServer V2 and V3 asset APIs use a per-request HMAC-SHA256 HTTP Signature with `Accept` and GMT `Date`; keep the host clock synchronized with NTP.

Existing `private_token_legacy` records continue to run only for migration. They are not returned by the configuration API and are not the default for new configurations. See [JumpServer Access Key authentication](docs/JumpServer访问密钥认证.md) for the API/KV contract and troubleshooting including 401 responses.

## PVE

PVE requires an HTTPS API base URL and a dedicated read-only privsep API Token. See [PVE接入说明](docs/PVE接入说明.md) for the encrypted Consul account record, task IDs, Guest Agent prerequisites, and JumpServer setup. Certificate and hostname verification are on by default, including existing account records without `tls_verify`. To use a self-signed endpoint temporarily, set that account's `tls_verify` to `false`; HTTP remains rejected and an alert without credentials is logged. Prefer adding the enterprise/private CA to the container or system trust store.

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
- If JumpServer returns 401, verify the Access Key has not been revoked, its source-IP allowlist includes this API host, and both system clocks are NTP-synchronized. Do not paste a Secret or legacy Token into logs, shell history, or issue comments.
- If `npm install` fails on an old runtime, use Node 14+ for this Vue CLI 4 project.
