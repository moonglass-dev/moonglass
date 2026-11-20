# Agent state

The agent's own database on each server. W2-13 creates it with the release tables, W2-3 uses it, W2-10a adds the command records, and W4-3b the auto-rollback records. The dependency rule is the ADR-0010 amendment of 2026-11-17 (owner decision D2, weeks 2–3 preflight).

## What it holds, and what it never holds

- **Release, command and auto-rollback records only:** which apps run here, their releases and release state, the commands the agent has executed, and the auto-rollback events it has raised.
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
| `command`, `volumes`, `labels`, `restart`, `healthcheck` | W6-1c. As sent (`docs/specs/protocol.md`, "Service settings"), in the JSON forms of `docs/specs/storage.md` ("Service settings on releases"), with the same defaults. The volume rule (`docs/specs/release.md`) and `REMOVE_APP` (`docs/specs/delete.md`) read `volumes`. |
| `has_pre_deploy`, `pre_deploy_reversible` | As sent. `pre_deploy_reversible` is filled by `RunPreDeploy`. |
| `status` | `candidate`, `current`, `previous`, `retired`, `failed` or `aborted`. |
| `container_id` | The release's container, or empty once removed. |
| `error` | The `detail` of a failed or rejected step. |
| `created_at`, `switched_at`, `stop_after` | `switched_at` is when the release became current. `stop_after` is when a previous or retired container is due to be stopped (`docs/specs/release.md`, "Switch"). |

### `commands` (W2-10a)

| Column | Meaning |
|---|---|
| `request_id` | Primary key. The envelope's request id (`docs/specs/connection.md`). |
| `type`, `app_id` | The command: `deploy`, `switch_release`, `rollback` or `run_pre_deploy` (and `build`, `stop_app`, `remove_app` from the tickets that add those commands). |
| `result` | The encoded `CommandResult` protobuf. `NOT NULL`: a row is written only with its result. |
| `received_at`, `finished_at` | When the executor started the command, and when it wrote the result. |

A command still running has no row. Which request ids are running is kept in memory by the agent (W2-10b). After an agent restart, a re-sent command with no row runs again; every executor step already handles what an interrupted run leaves behind (`docs/specs/release.md`, the leftover-container rules), and no state change was committed without its row.

**Executor API (W2-10a).** Each executor command method (`Deploy`, `SwitchRelease`, `Rollback`, `RunPreDeploy`) takes the envelope's request id. Every result it returns is recorded exactly once:

- When the command writes a state change (`RecordCandidate`, `RecordPreDeploy`, `RecordSwitch`, `RecordRollback`, `RecordAbort`, `RecordFailed`), the `commands` row is written in the same transaction.
- When it writes nothing else (a rejection before any write, a failed push, a failed commit), the row is written alone, in its own transaction, before the method returns.
- A request id that already has a row is not run: the method returns the stored `CommandResult` unchanged, and writes nothing.
- `DB.CommandResult(ctx, requestID)` returns the stored result and whether there is one.
- An empty request id is an error, and nothing runs.

### `auto_rollback_events` (W4-3b)

One row per fired trigger (`docs/specs/auto-rollback.md`, "Events and reports").

| Column | Meaning |
|---|---|
| `id` | Primary key. `a` plus 32 lowercase hex: the report's request id. |
| `app_id`, `release_id`, `previous_release_id` | The watched release and the release it would roll back to. |
| `trigger` | `restarts`, `health` or `error_rate`. |
| `action` | `rolled_back`, `shadow`, `held`, `skipped` or `failed`. |
| `detail` | The executor's `detail` for `failed`, otherwise empty. |
| `numbers` | JSON object: `restart_count`, `health_failing_seconds`, `window_requests`, `window_5xx`, `previous_requests`, `previous_5xx`, `previous_measured`, as in the report. |
| `app_state` | JSON object of the app's release state after the event, as in the report's `app`. |
| `detected_at`, `reported_at` | `reported_at` is null until the control plane acknowledged the report. |

### Auto-rollback columns (W4-3b)

- `releases.auto_rollback`: JSON object of the seven settings (`docs/specs/auto-rollback.md`, "Settings"), written by `RecordCandidate` from `Deploy.auto_rollback`, with the defaults when the `Deploy` had none. `NOT NULL`; the migration fills existing rows with the defaults.
- `apps.auto_rollback_hold`: integer 0 or 1, `NOT NULL DEFAULT 0` ("Never twice in a row").

## Rules

- A command's state change and its `commands` row are written in one transaction (`docs/specs/connection.md`, "Each request id runs once").
- **Retention.** Per app, the rows for the current and previous release and the three newest `retired` releases are kept: five in all, with their images (`docs/specs/release.md`, "Keep five"). Newest means `created_at`, then the order the rows were written. A `retired` row outside those three is deleted only once its stop has run (`stop_after` unset and `container_id` empty), so its container is never orphaned. `failed` and `aborted` rows are deleted after 7 days. `commands` rows are deleted 7 days after `finished_at` (W2-10a), longer than any re-send can arrive. `auto_rollback_events` rows are deleted 30 days after `reported_at`; unreported rows are kept (W4-3b). Retention runs at start and every hour.
- **A carried-out stop** is one transaction that clears the row's `stop_after`, and for a `retired` row also its `container_id` because the container was removed; a `previous` row keeps its `container_id`, so a rollback inside its window only starts it. It writes nothing and returns an error when the row is not `previous` or `retired`, is already recorded as stopped, or its stop is not yet due (`docs/specs/release.md`, "SwitchRelease").
- A `failed` release row never creates or changes an `apps` row: the binding and the release state stay as they were (`docs/specs/release.md`, "Failure").
- **A switch** (W2-4b) is one transaction: the candidate row becomes `current` with `switched_at` = now, the old current becomes `previous` with `stop_after` = now + 10 minutes, the old previous becomes `retired` with `stop_after` = now + 30 seconds, and the `apps` row takes the new state. Times are RFC 3339 UTC. A state that does not follow from the stored one writes nothing.
- **A rollback** (W2-4d) is one transaction too: the restored `previous` row becomes `current` with `switched_at` = now and `stop_after` cleared, the row rolled back from becomes `retired` with `stop_after` = now + 30 seconds, and the `apps` row takes the new state. The candidate row, the app's candidate and its phase are unchanged. Times are RFC 3339 UTC, and a state that does not follow from the stored one writes nothing.
- **A failed health gate** (W2-4b) writes one transaction too: the candidate row becomes `aborted` with the gate's detail as `error` and an empty `container_id`, and the `apps` row takes the state `Abort` returned. The `Current` and `Previous` rows are untouched.
- **An auto-rollback** (W4-3b) commits in one transaction: the rollback's state change exactly as "A rollback" above, the `auto_rollback_events` row with the action `rolled_back`, and `apps.auto_rollback_hold` = 1. Any other event (`shadow`, `held`, `skipped`, `failed`) is its row alone.
- **Clearing the hold** (W4-3b) sets `apps.auto_rollback_hold` = 0 for one app, when a watch ends at its 10 minutes with no trigger that fired.
- **Marking an event reported** sets `reported_at` once; marking it again changes nothing and is not an error.
- A database that does not open or migrate stops the agent with the error and the file's path.
