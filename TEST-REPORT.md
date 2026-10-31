# PVE / JumpServer QA remediation report

## Scope

- `REQ-PVE-006`: PVE API clients verify certificate chains and hostnames by default; only an account record with `tls_verify=false` may connect to a self-signed HTTPS endpoint. HTTP is always rejected.
- `BUG-002`: JumpServer V2/V3 asset payloads must read the existing `ecs_info` contract. V3 must include `protocols` and `accounts`.

## Regression coverage

`internal/pve/client_test.go` verifies default rejection of an untrusted certificate, success with a trusted private CA, explicit `tls_verify=false` access to a self-signed HTTPS endpoint, and HTTP rejection even when verification is disabled. It also confirms independent cloned transports and a warning that does not contain token material. `NewClient` only accepts an inspectable standard transport and clones it per account.

`internal/jumpserver/syncer_test.go` uses a Consul mock plus JumpServer HTTP mock to check both wire contracts:

- V2 sends `ip`, `hostname`, string `protocols`, `platform`, `admin_user`, `nodes`, `domain`, and `comment`.
- V3 sends `address`, `name`, protocol objects, `platform`, `accounts` with the configured template, `nodes`, `domain`, and `comment`.

The tests seed account-level `ecs_info`; this verifies the required account-level-over-global precedence. Empty or absent account-level configuration falls back to global `ecs_info` in production code.

## Verification commands

```bash
go test ./internal/pve ./internal/jumpserver -run '^(TestTLSVerificationModes|TestNewClientRejectsCleartextAPIURL|TestNewClientRejectsUninspectableTransport|TestV2MockContractPayloadUsesECSInfo|TestQAV3PayloadIncludesProtocolsAndAccounts)$' -count=1 -v
go test ./...
go vet ./...
go build ./cmd/tensuns ./cmd/consulmock
make smoke
```

All listed commands passed in the final validation run. `make smoke` reported `PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config`.
