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
- [Media](#media)
- [Reactions](#reactions)
- [Replies, edits and deletes](#replies-edits-and-deletes)
- [Groups](#groups)
- [Sending limits](#sending-limits)
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
| `PUBLIC_URL` | Public address of your server | — |
| `MEDIA_MAX_MB` | Largest file sent or downloaded, in MB (1 to 100). Files are held in memory while sent | `16` |
| `SEND_MIN_INTERVAL_MS` | Minimum time between two messages of the same instance, in milliseconds | `1000` |
| `SEND_MAX_PER_MINUTE` | Most messages one instance sends in any 60 seconds (`0` = no limit) | `30` |
| `SEND_MAX_PER_HOUR` | Most messages one instance sends in any 60 minutes (`0` = no limit) | `600` |
| `SEND_QUEUE_MAX` | Most send requests waiting for their turn per instance; more get `429` | `50` |
| `WEBHOOK_TIMEOUT` | Webhook request timeout, in seconds | `10` |
| `WEBHOOK_ALLOW_PRIVATE` | Allow webhooks to loopback and private networks (same host or Docker network). Keep `false` if other people can set webhooks; cloud metadata stays blocked | `false` |
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
| `POST` | `/instances/{id}/media/download` | Download a received file |
| `POST` | `/instances/{id}/messages/reaction` | React to a message |
| `POST` | `/instances/{id}/messages/edit` | Edit the text of a message you sent |
| `POST` | `/instances/{id}/messages/delete` | Delete a message for everyone |
| `GET` | `/instances/{id}/groups` | List groups |
| `POST` | `/instances/{id}/groups` | Create a group |
| `GET` | `/instances/{id}/groups/{jid}` | Group info with members |
| `PATCH` | `/instances/{id}/groups/{jid}` | Change name, description, `announce` or `locked` |
| `POST` | `/instances/{id}/groups/{jid}/participants` | Add, remove, promote or demote participants |
| `GET` | `/instances/{id}/groups/{jid}/invite` | Get the invite link |
| `POST` | `/instances/{id}/groups/{jid}/invite/reset` | Revoke the invite link and make a new one |
| `POST` | `/instances/{id}/groups/join` | Join a group with an invite link |
| `POST` | `/instances/{id}/groups/{jid}/leave` | Leave a group |
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
| `message.status` | A message you sent was `delivered`, `read` or `played` (voice notes and videos) |
| `connection.update` | The instance connected, disconnected or needs a new QR code |
| `group.update` | Group name, description, settings or members changed, or this number joined a group |
| `call.received` | Incoming call (logged; calls are not answered by the API) |

`message.received` carries `sender`/`chat` as WhatsApp sends them — often a LID (`…@lid`), which is not a phone number — plus `senderPn` and, in private chats, `chatPn` with the phone-number JID when it is known. `chatType` says what kind of chat it is: `private`, `group`, `status` (WhatsApp Status), `broadcast` or `newsletter` (a Channel) — filter out the ones you do not handle. Deleted and edited messages arrive with `type` `revoke` or `edit` and the affected message id in `target`; reactions arrive with `type` `reaction`, the emoji in `text` (empty when the reaction was removed) and the message reacted to in `target`. A message that answers an earlier one carries `replyTo` with that message's `id` and `participant`.

`message.status` carries the message `ids`, the `chat` and the `status`. In a group every participant sends their own receipt, identified by `participant` and `participantPn`; in a private chat, `chatPn` has the phone number when it is known.

The webhook URL must be an `http` or `https` address that does not point to loopback, private or link-local networks (see `WEBHOOK_ALLOW_PRIVATE`). The secret you send is never echoed back; if you leave it empty, one is generated and shown once.

Every request carries an `X-FireZAP-Signature` header (HMAC-SHA256 of the body with your
webhook secret), so you can confirm it came from your server. Failed deliveries are retried
with exponential backoff.

## Media

`message.received` for images, videos, audio, documents and stickers carries a `media` object
(mimetype, size, fileName, seconds, `ptt` for voice notes, and the keys to download the file).
The API keeps no copy of messages or files: to get the file, send that object back unchanged:

```bash
curl -X POST https://api.example.com/instances/my-number/media/download   -H "Authorization: Bearer INSTANCE_KEY" -H "Content-Type: application/json"   -d '<the "media" object from the webhook>' -o file
```

The response is the decrypted file with its `Content-Type`. WhatsApp keeps media for a limited
time: an old file answers `410 Gone`, so download what you need when it arrives.

To send, use `POST /instances/{id}/messages/media` with `to`, `type` (`image`, `video`,
`audio` or `document`) and the file in `url` **or** `base64` (a `data:` URL is accepted),
plus optional `caption`, `fileName`, `mimetype` and `ptt`:

```bash
curl -X POST https://api.example.com/instances/my-number/messages/media   -H "Authorization: Bearer INSTANCE_KEY" -H "Content-Type: application/json"   -d '{"to": "5511999999999", "type": "document", "url": "https://example.com/invoice.pdf", "caption": "Your invoice"}'
```

Images must be JPEG or PNG and videos MP4; voice notes (`"ptt": true`) must be OGG/Opus —
other formats are refused instead of arriving broken on the phone, so send them as a document.
Media URLs follow the same address rules as webhooks. Files are limited by `MEDIA_MAX_MB`.

## Reactions

React with `POST /instances/{id}/messages/reaction`, using the values of the `message.received`
event: `chat`, the message `id` as `messageId`, and `fromMe` when the message was sent by your
number. In a group, reacting to someone else's message also needs their `sender` as
`participant`. An empty `emoji` removes your reaction.

```bash
curl -X POST https://api.example.com/instances/my-number/messages/reaction \
  -H "Authorization: Bearer INSTANCE_KEY" -H "Content-Type: application/json" \
  -d '{"chat": "5511999999999@s.whatsapp.net", "messageId": "3EB0F1E313827D8CDEB5AE", "emoji": "👍"}'
```

## Replies, edits and deletes

**Reply** — add `replyTo` to a text or media message: the `messageId` being answered, `fromMe`
when it was sent by your number, `participant` for someone else's message in a group, and
`text` to show inside the reply bubble. The API keeps no messages, so it cannot fill the quoted
text by itself — without `text` the phone shows the reply with an empty quote.

```bash
curl -X POST https://api.example.com/instances/my-number/messages/text \
  -H "Authorization: Bearer INSTANCE_KEY" -H "Content-Type: application/json" \
  -d '{"to": "5511999999999", "text": "Yes, it is in stock!", "replyTo": {"messageId": "3EB0F1E313827D8CDEB5AE", "text": "Do you have it in blue?"}}'
```

**Edit** — `POST /instances/{id}/messages/edit` with `chat`, `messageId` and the new `text`.
Only text messages your number sent; WhatsApp accepts edits for about 15 minutes after sending
and ignores later ones.

**Delete for everyone** — `POST /instances/{id}/messages/delete` with `chat`, `messageId` and
`fromMe: true` for your own messages. In a group where your number is an admin, you can also
delete someone else's message by sending `participant`. WhatsApp limits how old a message can be.

Replies, edits and deletes go through the same queue and limits as any other message.

## Groups

`{jid}` is the group id (`120363…@g.us`, or just the digits). Changes need this number to be
a group admin, as on the phone.

- **Create** — `POST /instances/{id}/groups` with `name` (up to 100 characters) and, optionally,
  `participants` (phone numbers or user ids). Without participants the group starts with your
  number only: share the invite link instead.
- **Members** — `POST /instances/{id}/groups/{jid}/participants` with `action` (`add`, `remove`,
  `promote` or `demote`) and up to 50 `participants`. WhatsApp answers per person: check
  `error` in each item — `403` usually means their privacy settings do not allow being added,
  so send them the invite link.
- **Settings** — `PATCH /instances/{id}/groups/{jid}` with any of `name`, `description`,
  `announce` (only admins send messages) and `locked` (only admins edit the group info).
- **Invite links** — `GET …/invite` returns the link; `POST …/invite/reset` revokes it and
  returns a new one. `POST /instances/{id}/groups/join` with `link` joins a group.

`group.update` reports changes with only the fields that changed: `name`, `description`,
`announce`, `locked`, `join`, `leave`, `promote`, `demote`, `inviteReset` (the link itself
is never sent), and `joined: true` when this number joins or is added to a group.

> [!WARNING]
> Adding people who did not ask to be in a group is the most reported kind of WhatsApp spam.
> **Every person added counts as one message** in the sending limits (creating a group with 20
> people counts as 21), and a request larger than `SEND_MAX_PER_MINUTE` is refused. Prefer
> invite links.

## Sending limits

Every message an instance sends — text, media, reaction, or a group change — goes through one queue per
instance: one at a time, in arrival order, respecting `SEND_MIN_INTERVAL_MS`,
`SEND_MAX_PER_MINUTE` and `SEND_MAX_PER_HOUR`. Each instance has its own counters, so a busy
number never slows down the others.

A request waits for its turn while that fits in its time limit. When the queue is full
(`SEND_QUEUE_MAX`) or the limits would hold the message too long, the API answers
`429 Too Many Requests` with a `Retry-After` header and `retryAfter` (seconds) in the body —
**nothing was sent**, so it is safe to try again later. `GET /instances/{id}` shows the current
activity in `sending`: `queued`, `lastMinute` and `lastHour`.

The defaults suit customer service. They are a protection, not a guarantee: WhatsApp decides
on its own what it considers abuse, and no limit makes unsolicited bulk messages safe.

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
emulator farms). What it includes, to protect you and your contacts:

- Sending limits per instance: minimum interval, per minute and per hour (see [Sending limits](#sending-limits))

Planned:

- Gradual limits for newly connected numbers
- Opt-out handling: contacts who reply with a stop word are not messaged again
- Clear logs of everything that was sent

**Only message people who asked to hear from you.** You are responsible for how you use this
software and for complying with WhatsApp's terms and with privacy laws such as the LGPD (Brazil)
and the GDPR (EU).

## Roadmap

- [x] **v0.1** — Instances (QR and pairing code), text messages, receiving messages, webhooks, persistent sessions
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
