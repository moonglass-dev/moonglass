# Agent state

The agent's own database on each server. W2-13 creates it with the release tables, W2-3 uses it, and W2-10 adds the command records. The dependency rule is the ADR-0010 amendment of 2026-11-17 (owner decision D2, weeks 2–3 preflight).

## What it holds, and what it never holds

- **Release and command records only:** which apps run here, their releases and release state, and the commands the agent has executed.
- **No key material, ever:** no private key, no wrapped or unwrapped environment key, no agent X25519 or Ed25519 key, no deploy key, no token, no secret value or ciphertext, and no registry credential. Those stay under `internal/keys`, in its own files under the agent's data directory (`docs/specs/enrolment.md`, `docs/specs/secrets.md`, `docs/specs/build.md`).
- If a later ticket needs key material in this database, that table moves into trusted core first, through an ADR-0010 amendment. It is never added here.
- No environment variable value. An `error` column holds the same text as the command's `detail`, which never contains one (ADR-0004 decision 12).

## Package and file

- `internal/agent/state` is the only package that opens the database. It is the only SQL outside `internal/store`, and it is not trusted core.
- Driver: `modernc.org/sqlite` at the version in ADR-0010. The file is `<data-dir>/agent.db`, `/var/lib/moonglass/agent/agent.db` under systemd, mode 0600.
- Migrations are numbered plain SQL files in `internal/agent/state/migrations`, applied with `store.Migrate` from `internal/store` (the root package, which imports no driver). There is no second runner.
- Pragmas at open: `journal_mode=WAL`, `synchronous=FULL` (an executed-command record must survive a power cut, `docs/specs/connection.md`), `busy_timeout=5000`, `foreign_keys=ON`, and `cache_size=-2000` (2 MB, within the agent's SQLite budget in stack §6).

## Schema

Timestamps are UTC RFC 3339 text with a `Z`, as in `docs/specs/storage.md`. There is no `org_id`: one agent belongs to one org, and nothing here reaches the control plane's tables.

### `apps` (W2-13)

One row per `app_id` that has ever been deployed here.

| Column | Meaning |
|---|---|
| `app_id` | Primary key. |
| `project`, `environment`, `service` | The binding made at the first `Deploy` (`docs/specs/protocol.md`, "Identity binding"). Never updated. |
| `current_release_id`, `previous_release_id`, `candidate_release_id` | `release.State`, nullable. |
| `phase` | `none`, `pending` or `ready`. |
| `created_at` | |

### `releases` (W2-13)

| Column | Meaning |
|---|---|
| `app_id`, `release_id` | Primary key together. |
| `image_ref` | The reference as sent in `Deploy`. |
| `image_id` | The image ID the container runs. |
| `repo_digest` | The registry digest the reference resolved to, or empty for an image built here. For a tag it is the digest the pull resolved to at deploy time (owner condition on D4). |
| `port`, `health_path`, `domains` | As sent. `domains` is a JSON array. |
| `has_pre_deploy`, `pre_deploy_reversible` | As sent. `pre_deploy_reversible` is filled by `RunPreDeploy`. |
| `status` | `candidate`, `current`, `previous`, `retired`, `failed` or `aborted`. |
| `container_id` | The release's container, or empty once removed. |
| `error` | The `detail` of a failed or rejected step. |
| `created_at`, `switched_at`, `stop_after` | `stop_after` is when a previous or retired container is due to be stopped (`docs/specs/release.md`, "Switch"). |

### `commands` (W2-10)

| Column | Meaning |
|---|---|
| `request_id` | Primary key. |
| `type`, `app_id` | The command. |
| `result` | The encoded `CommandResult` protobuf, or null while the command runs. |
| `received_at`, `finished_at` | |

## Rules

- A command's state change and its `commands` row are written in one transaction (`docs/specs/connection.md`, "Each request id runs once").
- **Retention.** Per app, the rows for the current and previous release and the three newest `retired` releases are kept: five in all, with their images (`docs/specs/release.md`, "Keep five"). Older `retired` rows are deleted with their images. `failed` and `aborted` rows, and `commands` rows, are deleted after 7 days. Retention runs at start and every hour.
- A `failed` release row never creates or changes an `apps` row: the binding and the release state stay as they were (`docs/specs/release.md`, "Failure").
- A database that does not open or migrate stops the agent with the error and the file's path.
