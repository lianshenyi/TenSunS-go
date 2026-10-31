# PVE / JumpServer QA remediation report

## Scope

- `BUG-001`: PVE API clients must never send API tokens through a client that disables certificate or hostname verification.
- `BUG-002`: JumpServer V2/V3 asset payloads must read the existing `ecs_info` contract. V3 must include `protocols` and `accounts`.

## Regression coverage

`internal/pve/client_test.go` verifies that a cleartext PVE URL, an `InsecureSkipVerify=true` transport, and an uninspectable custom transport are all rejected. `NewClient` clones an accepted standard transport, including its TLS configuration, so subsequent mutation of the caller's transport cannot weaken the client's verification settings.

`internal/jumpserver/syncer_test.go` uses a Consul mock plus JumpServer HTTP mock to check both wire contracts:

- V2 sends `ip`, `hostname`, string `protocols`, `platform`, `admin_user`, `nodes`, `domain`, and `comment`.
- V3 sends `address`, `name`, protocol objects, `platform`, `accounts` with the configured template, `nodes`, `domain`, and `comment`.

The tests seed account-level `ecs_info`; this verifies the required account-level-over-global precedence. Empty or absent account-level configuration falls back to global `ecs_info` in production code.

## Verification commands

```bash
go test ./internal/pve ./internal/jumpserver -run '^(TestQATLSBoundaryRejectsVerificationBypass|TestNewClientRejectsUninspectableTransport|TestV2MockContractPayloadUsesECSInfo|TestQAV3PayloadIncludesProtocolsAndAccounts)$' -count=1 -v
go test ./...
go vet ./...
go build ./cmd/tensuns ./cmd/consulmock
make smoke
```

All listed commands passed in the final validation run. `make smoke` reported `PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config`.
