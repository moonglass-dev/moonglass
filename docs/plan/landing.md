# Moonglass — Landing copy

_The copy for moonglass.dev. The site repository renders this text (W8-S); change it here first, then on the site. It follows the voice in `vision.md` §1.2 and the prices in `docs/specs/pricing.md`. The status section and the FAQ say what works at each milestone, so the site never claims more than has shipped._

## Hero

**Every app on your servers, in one calm place.**

Moonglass deploys, watches and rolls back everything you run, without dropping a request.

Buttons: **Join the waitlist** · **Read the plan**

A dashboard mock beside the copy: the sidebar (Inbox, Activity, Servers, Agents, Projects), a project with its tabs (Overview, Deployments, Logs, Variables, Settings), a pending deploy from a coding agent with its diff and Approve, and the live runtime panel.

## Deploys

**Deploys that finish what they start.**

1. **Enroll a server.** Any Linux box with Docker. One command, and the server dials out to Moonglass.
2. **Push or deploy.** From a commit, a Dockerfile, Railpack, Compose or an image.
3. **Roll back in one click.** The previous release is still warm, so it takes under five seconds.

- **Every release earns its traffic.** Build, health check, switch, drain, watch. Zero failed requests across ten deploys, measured in public.
- **Rolls itself back.** A crash loop, a failed health check or a raised error rate brings the last good release back.
- **Bring your Compose file.** See the plan first: what is created, changed, left alone or restarted. Nothing changes until you say yes.
- **Every pull request gets a preview,** with its own URL and database, removed when the pull request closes.
- **A disk that can't fill.** Builds stop and caches are pruned before your apps are affected.

## Architecture

**The control plane never SSHes into your servers.**

Moonglass turns the connection around. Each server runs a small Moonglass agent that dials out over mutual TLS, and the control plane can only ask it for a short, typed list of things.

- **0 SSH keys.** We hold none.
- **Typed commands.** No shell strings, ever.
- **Policy lives on the server.** `/etc/moonglass/agent.toml` decides what may run there, and the control plane cannot change it.
- **Secrets stay sealed.** Values are encrypted before they leave your browser or terminal.
- **Under 100 MB.** Agent and proxy together, on a 2 GB VPS with room for your apps.

## Coding agents

**Your coding agent proposes. You approve.**

Give a coding agent a token scoped to an environment. It deploys to staging on its own, over the CLI or MCP. When it wants production, the deploy waits in your Inbox with the diff: the image, the commits, the variables it changes and the services it restarts. Approve, and it ships.

Three cases on the page:
- **Allowed:** a staging deploy by the coding agent goes live.
- **Held:** its production deploy waits for a person, with the diff.
- **Refused:** a delete on a server whose `agent.toml` forbids it is refused by the server itself.

## Everything you expect

Push-to-deploy from GitHub · Dockerfile, Railpack and Compose · PR previews · Domains and automatic HTTPS · Variables and secrets · One-click Postgres, MySQL, MariaDB, MongoDB and Redis · Backups to any S3 bucket and one-click restore · Logs and metrics · Many servers · Instant and automatic rollback · Webhooks, Slack, Discord, Telegram and email · Teams and roles · API, CLI and MCP · One-click templates · Import from Coolify

## Pricing

**Free means free. Cloud costs less than a coffee.**

| | Free | Cloud | Business |
|---|---|---|---|
| Price | $0 forever | $4/mo with 2 servers, +$2/mo per server | $29/mo |
| Control plane | On your server | We run it | Ours or yours |
| People, apps, deploys, backups | Unlimited | Unlimited | Unlimited |
| Teams, roles, approvals, previews, MCP | Included | Included | Included |
| SSO, SCIM, audit export | | | Included |
| Support | Community | Email | Priority |

- No seat prices. No usage surprises: the only meter is the servers you connect, and the price shows before you add one.
- **Founding members** pay $3/mo on Cloud ($1.50 per extra server) and $19/mo on Business, for as long as they stay.
- Apache-2.0. A lapsed plan never stops your apps.

## Status

**Where Moonglass is today.**

- **Now:** pre-alpha, not ready for production. The plan and the code are public.
- **Alpha:** self-hosted, from the waitlist. Install, connect a repository, push to deploy, set secrets, roll back and upgrade, all in the dashboard.
- **Launch:** open to everyone, with previews, Compose, databases and restore, teams, approvals, MCP, templates and the Coolify import.
- **v1:** Cloud and Business open, founding members first.

## Questions

**What works today, and when does Moonglass ship?**
Nothing is ready for production yet. The alpha opens to the waitlist first, then the public launch, then v1 with Cloud. There are no dates: each step ships when its demo passes on real servers.

**Is the free version limited?**
No. Self-hosted Moonglass has every feature, for any number of people, servers and apps. Business adds SSO, SCIM, audit export and priority support, and Cloud runs the control plane for you.

**Can I move between Cloud and self-hosted later?**
Yes, in both directions, with `moonglass export` or the Export button. Your servers keep running your apps while you move.

**How is this different from Coolify or Dokploy?**
Moonglass never SSHes into your servers, never drops a request on deploy, keeps secrets encrypted end to end, and lets a person approve what a coding agent proposes. The comparison page credits what they do well.

**What happens if I stop paying?**
Your apps keep running and you can always export. A lapsed plan only stops new servers and members from being added.
