<div align="center">

# FireZAP API

**Open-source WhatsApp API with a built-in dashboard — instances, groups, webhooks and more.**

**English** · [Português (Brasil)](README.pt-BR.md)

![status](https://img.shields.io/badge/status-early%20development-orange)
![license](https://img.shields.io/badge/license-Apache%202.0-blue)
![go](https://img.shields.io/badge/Go-1.23+-00ADD8)

</div>

> [!WARNING]
> **This project is in early development.** The features below describe the plan for the first
> releases — they are not available yet. Follow the [roadmap](#roadmap) and watch the repository
> for the first release.

---

## Table of contents

- [What is FireZAP API](#what-is-firezap-api)
- [Features](#features)
- [Architecture](#architecture)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [API overview](#api-overview)
- [Webhooks](#webhooks)
- [Dashboard](#dashboard)
- [Responsible use](#responsible-use)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)

---

## What is FireZAP API

FireZAP API is a self-hosted HTTP API to connect WhatsApp numbers to your own systems: CRMs,
chatbots, help desks, e-commerce and automations. You run it on your own server (VPS), keep your
data with you, and manage everything from a web dashboard.

It is built and maintained by the team behind [FireZAP](https://firezap.com.br) and released to
the community for free, under the Apache 2.0 license.

**Two connection modes, one API:**

| Mode | How it connects | Best for |
|---|---|---|
| **Web (QR code / pairing code)** | Multi-device protocol, like WhatsApp Web, powered by [whatsmeow](https://github.com/tulir/whatsmeow) | Getting started quickly, small and medium volumes |
| **Cloud API (official)** | Meta's official WhatsApp Business Platform | Guaranteed stability, high volume, compliance |

Your code talks to the same endpoints in both modes — switching a number from one mode to the other
does not require rewriting your integration.

## Features

Planned for the first releases:

**Instances**
- Unlimited instances (one per WhatsApp number), each fully isolated — a failure in one never affects the others
- Connect with QR code or pairing code (phone number)
- Session stored in the database: survives restarts without scanning the QR code again
- Automatic reconnection and health monitoring, with alerts when an instance drops

**Messages**
- Text, images, video, documents, audio (including voice notes) and stickers
- Reactions, replies (quoted messages), edit and delete
- Delivery and read receipts
- Per-instance send queue with configurable rate limits

**Groups**
- Create groups, update name, description and picture
- Add, remove, promote and demote participants
- Invite links: create, revoke and join
- Read group metadata and participant lists

**Integrations**
- Webhooks for every event, with retries and signature verification
- REST API with an OpenAPI (Swagger) specification
- API keys per instance and a global admin key

**Dashboard**
- Web dashboard to manage instances, scan QR codes, view logs and configure webhooks
- Available in **English** and **Portuguese (Brazil)**

## Architecture

```
┌──────────────┐     REST + API key      ┌───────────────────────────────┐
│ Your system  │ ──────────────────────▶ │          FireZAP API          │
│ (CRM, bot…)  │ ◀────────────────────── │  ┌─────────┐   ┌───────────┐  │
└──────────────┘        Webhooks         │  │ Queue   │   │ Dashboard │  │
                                         │  └────┬────┘   └───────────┘  │
                                         │       ▼                       │
                                         │  ┌──────────────────────────┐ │
                                         │  │ Providers                │ │
                                         │  │  • Web (whatsmeow)       │ │
                                         │  │  • Cloud API (Meta)      │ │
                                         │  └──────────────────────────┘ │
                                         └───────────────┬───────────────┘
                                                         ▼
                                                   PostgreSQL
                                       (sessions, instances, message log)
```

- **Language:** Go — low memory per instance and a single binary
- **Database:** PostgreSQL
- **Media:** local disk or any S3-compatible storage
- **Deploy:** Docker image and `docker compose`

## Quick start

> Available with the first release.

```bash
git clone https://github.com/firezaprepo/firezap-api.git
cd firezap-api
cp .env.example .env        # set FZ_ADMIN_KEY and the database password
docker compose up -d
```

Open `http://YOUR_SERVER:8080` to access the dashboard and create your first instance.

**Minimum server (VPS):** 1 vCPU, 1 GB RAM and 10 GB of disk for a few instances. Add about
50 MB of RAM per connected instance.

## Configuration

All settings come from environment variables (`.env`). The full list will live in
`.env.example`; the main ones:

| Variable | Description | Default |
|---|---|---|
| `FZ_ADMIN_KEY` | Admin key for the dashboard and admin endpoints. **Required.** | — |
| `DATABASE_URL` | PostgreSQL connection string. **Required.** | — |
| `HTTP_PORT` | Port for the API and the dashboard | `8080` |
| `PUBLIC_URL` | Public address of your server (used in media links) | — |
| `MEDIA_STORAGE` | `local` or `s3` | `local` |
| `WEBHOOK_TIMEOUT` | Webhook request timeout, in seconds | `10` |
| `DEFAULT_LANGUAGE` | Dashboard language: `en` or `pt-BR` | `en` |
| `LOG_LEVEL` | `debug`, `info`, `warn` or `error` | `info` |

> [!IMPORTANT]
> Never commit your `.env` file. Use a long random value for `FZ_ADMIN_KEY` and serve the
> dashboard over HTTPS (a reverse proxy such as Caddy or Nginx).

## API overview

All requests use `Authorization: Bearer <key>` — the admin key for admin routes, or the
instance key for instance routes. The complete reference will be published as an OpenAPI file.

| Method | Route | Description |
|---|---|---|
| `POST` | `/instances` | Create an instance |
| `GET` | `/instances` | List instances |
| `POST` | `/instances/{id}/connect` | Start the connection (generates the QR code when not linked) |
| `GET` | `/instances/{id}/qr` | Get the QR code to connect |
| `POST` | `/instances/{id}/pair` | Connect with a pairing code (phone number) |
| `POST` | `/instances/{id}/logout` | Disconnect the number |
| `DELETE` | `/instances/{id}` | Delete the instance |
| `POST` | `/instances/{id}/messages/text` | Send a text message |
| `POST` | `/instances/{id}/messages/media` | Send image, video, document or audio |
| `POST` | `/instances/{id}/messages/reaction` | React to a message |
| `GET` | `/instances/{id}/groups` | List groups |
| `POST` | `/instances/{id}/groups` | Create a group |
| `POST` | `/instances/{id}/groups/{jid}/participants` | Add, remove, promote or demote participants |
| `PUT` | `/instances/{id}/webhook` | Configure the webhook |

Example — send a text message:

```bash
curl -X POST https://api.example.com/instances/my-number/messages/text \
  -H "Authorization: Bearer INSTANCE_KEY" \
  -H "Content-Type: application/json" \
  -d '{"to": "5511999999999", "text": "Hello from FireZAP API!"}'
```

## Webhooks

Each instance can send events to a URL of your choice:

| Event | When |
|---|---|
| `message.received` | A new message arrives |
| `message.status` | A message you sent was delivered or read |
| `connection.update` | The instance connected, disconnected or needs a new QR code |
| `group.update` | Group name, participants or settings changed |
| `call.received` | Incoming call (logged; calls are not answered by the API) |

Every request carries an `X-FireZAP-Signature` header (HMAC-SHA256 of the body with your
webhook secret), so you can confirm it came from your server. Failed deliveries are retried
with exponential backoff.

## Dashboard

The dashboard runs together with the API, on the same port, and lets you:

- Create, connect and remove instances (QR code or pairing code)
- See the status of each instance in real time
- Configure webhooks and API keys
- Read recent logs and failed webhook deliveries

The interface is available in **English** and **Portuguese (Brazil)** — choose it in the
dashboard or set `DEFAULT_LANGUAGE`.

## Responsible use

> [!CAUTION]
> **FireZAP API is not affiliated with, endorsed by or connected to WhatsApp or Meta.**
> "WhatsApp" is a trademark of its owners.

The **Web mode** connects as a linked device, like WhatsApp Web, which is **not an official
WhatsApp product**. WhatsApp may restrict or ban numbers that break its
[Terms of Service](https://www.whatsapp.com/legal/terms-of-service), especially for unsolicited
bulk messages. If you need guaranteed stability and scale, use the **Cloud API mode**
(official).

This project is meant for legitimate customer communication. It **does not** and **will not**
include features to evade WhatsApp's anti-abuse systems (device spoofing, fake activity,
emulator farms). What it does include, to protect you and your contacts:

- Rate limits per instance and gradual limits for newly connected numbers
- Opt-out handling: contacts who reply with a stop word are not messaged again
- Clear logs of everything that was sent

**Only message people who asked to hear from you.** You are responsible for how you use this
software and for complying with WhatsApp's terms and with privacy laws such as the LGPD (Brazil)
and the GDPR (EU).

## Roadmap

- [ ] **v0.1** — Instances (QR and pairing code), text messages, receiving messages, webhooks, persistent sessions
- [ ] **v0.2** — Media, audio, reactions, receipts, send queue with rate limits
- [ ] **v0.3** — Groups (create, manage participants, invite links)
- [ ] **v0.4** — Dashboard (English and Portuguese)
- [ ] **v0.5** — Cloud API (official) mode behind the same endpoints
- [ ] **v1.0** — OpenAPI reference, Docker image, production hardening

Ideas and priorities are discussed in [Issues](https://github.com/firezaprepo/firezap-api/issues).

## Contributing

Contributions are welcome! Before opening a pull request:

1. Open an issue describing the bug or the feature
2. Fork the repository and create a branch from `main`
3. Run `go test ./...` and `go vet ./...` before committing
4. Keep code, comments and commit messages in English

Issues and discussions can be written in English or Portuguese.

## Security

Found a security issue? **Please do not open a public issue.** Report it privately through
[GitHub Security Advisories](https://github.com/firezaprepo/firezap-api/security/advisories/new).

## License

Released under the [Apache License 2.0](LICENSE).

Built on top of [whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0) — thank you to its
maintainers and contributors.
