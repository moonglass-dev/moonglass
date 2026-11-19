# One-click Postgres and Redis

This is the behaviour spec for managed Postgres and Redis (ticket W6-1). They are ordinary services created through the normal deploy path (`docs/specs/release.md`), not a separate runtime. The owner approved this spec on 2026-11-18.

## Images and placement

Postgres is `postgres:18`. Redis is `redis:7`. Each is created as an ordinary service in a project and environment, with that image pinned, on one server.

## Secrets, volume and health

A generated password is stored as a secret (`docs/specs/secrets.md`). Data lives on a named volume. Health checks are `pg_isready` for Postgres and `redis-cli ping` for Redis. Restart policy is `unless-stopped`.

## Out of scope

No backups (W6-2). No replication or pooling. No automatic major upgrades.

## URLs

`DATABASE_URL` / `REDIS_URL` is printed once at creation.
