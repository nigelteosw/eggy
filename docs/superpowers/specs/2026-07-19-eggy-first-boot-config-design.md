# Eggy First-Boot Configuration Design

## Goal

Eggy must start on a fresh Railway Volume without requiring an operator to upload `/data/config.yaml`. Its first-boot behavior should follow OpenClaw's useful pattern: apply safe defaults, collect the small amount of deployment-specific configuration from environment variables, validate the result, persist a canonical config file, and use that file on later starts.

## Configuration precedence

`/data/config.yaml` remains Eggy's canonical non-secret configuration.

1. If the configured file exists, Eggy loads it and never regenerates or overwrites it.
2. If the file is missing, Eggy constructs a version 1 configuration from first-boot environment variables and safe defaults.
3. Eggy validates the complete candidate before writing anything.
4. Eggy writes the validated YAML with mode `0600` using an atomic same-directory replacement while holding the config file lock.
5. Eggy then loads the persisted file through the normal strict loader.

An existing malformed or invalid file is an operator error. Eggy reports the validation error and does not replace it with defaults.

Secrets remain Railway service variables or local `.env` values. Eggy never writes provider tokens, OAuth client secrets, GitHub credentials, or encryption keys into `config.yaml`.

## First-boot variables

First boot supports these non-secret environment variables:

- `EGGY_TELEGRAM_OWNER_ID` is required and must be a positive integer.
- `EGGY_PUBLIC_BASE_URL` is optional when Railway supplies `RAILWAY_PUBLIC_DOMAIN`. If set, it must be an HTTP(S) URL. Otherwise Eggy derives `https://<RAILWAY_PUBLIC_DOMAIN>`.
- `EGGY_REPOSITORY_URL` enables the initial repository when present.
- `EGGY_REPOSITORY_NAME` defaults to `eggy` when a repository URL is present.
- `EGGY_REPOSITORY_BASE_BRANCH` defaults to `main`.
- `EGGY_REPOSITORY_PROTECTED_BRANCHES` defaults to the base branch and accepts a comma-separated branch list.

The first-boot path intentionally does not accept an entire YAML document through an environment variable. This keeps Railway setup understandable and prevents two competing full-configuration sources.

## Safe generated defaults

The generated configuration uses:

- `data_dir: /data`
- Telegram webhook path `/webhooks/telegram`
- DeepSeek adapters with `deepseek-v4-flash` and `deepseek-v4-pro`
- escalation after four tool steps or two recoverable failures
- runner root `/tmp/runs`, 45-minute timeout, 30-minute retention, 1 MiB output cap, and the existing conservative environment allowlist
- 30-minute heartbeat cadence, `22:00` to `07:00` quiet hours in `Asia/Singapore`, two-hour proactive minimum, and five proactive turns per week
- Calendar disabled until the owner explicitly enables and configures it
- no repository when `EGGY_REPOSITORY_URL` is absent

An absent repository keeps ordinary Telegram assistant behavior available without requiring `GITHUB_TOKEN` or Codex repository setup on the first healthy deployment.

## Railway runtime port

Railway healthchecks target the injected `PORT`. After loading either an existing or generated configuration, a non-empty `PORT` overrides only `server.listen` as `:<PORT>`. The port must be numeric and between 1 and 65535. Outside Railway, `server.listen` continues to control the address.

The runtime port override is not persisted into `config.yaml`, because it belongs to the deployment environment rather than the canonical operator configuration.

## Errors and recovery

When first-boot input is missing or invalid, Eggy exits with an error naming the exact variable and does not leave a partial config file. After the variable is corrected, the next restart retries generation.

If persistence fails because `/data` is missing, read-only, or full, Eggy reports the filesystem error. It does not fall back to ephemeral configuration because that would make state behavior differ across container replacements.

If two processes race to initialize the same path, the config lock serializes them. The second process loads the file created by the first rather than replacing it.

## Testing

Focused bootstrap tests will prove:

- a valid environment generates a strict version 1 config with mode `0600`;
- Railway's public domain is used when an explicit public URL is absent;
- optional repository variables produce the expected repository and defaults;
- Calendar is disabled and repositories are empty by default;
- missing or invalid required input fails without creating the file;
- an existing config is never overwritten;
- concurrent initialization preserves one complete valid file;
- a valid `PORT` overrides the runtime listen address, while an invalid port is rejected;
- secrets never appear in the generated YAML.

The focused bootstrap package test runs before the repository-wide `make fmt vet test race build` matrix. Docker smoke remains a separate check when a Docker daemon is available and must exercise an empty mounted `/data` directory.

## Documentation and compatibility

`config.example.yaml` remains the reference for advanced/manual configuration. The README Railway path changes from manually uploading `config.yaml` to setting first-boot variables and mounting `/data`.

This change does not alter `/data/state.json` or its schema. Existing deployments with `/data/config.yaml` behave as before except for the validated Railway `PORT` override.
