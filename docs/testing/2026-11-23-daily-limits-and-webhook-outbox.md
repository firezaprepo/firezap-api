# Test — daily limits and webhook outbox (2026-11-23)

Same setup as the [v0.1 end-to-end test](2026-11-10-e2e-v0.1.md): Linux server with Docker and
PostgreSQL 17, one instance linked to a real personal number (linked 13 days before), a receiver
that checks signatures and logs only metadata. **No message was sent to anyone**: the daily
limit was exercised with `POST /contacts/check` of the number itself (a lookup counts as one
unit), and the pause with numbers that are not on WhatsApp (only looked up, never messaged).

## Results

| Area | Result |
|---|---|
| Upgrade of an existing database | OK — `linked_at` added and filled from `created_at` for the linked instance; `fz_send_daily`, `fz_known_chats` and `fz_webhook_outbox` created; session restored without a new pairing |
| Events through the outbox | OK — `connection.update`, `message.received` and `message.status` delivered with a valid signature; the outbox empty after each delivery (nothing kept) |
| Receiver down (container stopped) | OK — events stayed `pending` with 3 attempts and the last error; none lost |
| `GET /webhook/failures` | OK — 4 failed events listed with event, attempts and last error, no body |
| `POST /webhook/failures/retry` with `{"ids": [6]}` | OK — `{"requeued": 1}` |
| `POST /webhook/failures/discard` without a body | OK — `{"discarded": 3}` (every remaining one) |
| Failure routes without a key | OK — 401 |
| Receiver back | OK — the requeued event was delivered 30 s later (one more attempt failed while the receiver was starting) |
| `SEND_DAILY_SCHEDULE=3` (temporary), `TZ=America/Sao_Paulo` | OK — 3 lookups 200, the 4th **429** "daily sending limit … (3 today, day 13 since it was linked)", `retryAfter` 40124 s = the next midnight in São Paulo |
| Restart with the limit reached | OK — the first lookup after the restart was refused: today's counter comes from the database |
| `SEND_PAUSE_AFTER_FAILURES=2`, `SEND_PAUSE_MINUTES=1` (temporary) | OK — two sends to numbers not on WhatsApp answered 422; the third **429** "sending is paused …" with `retryAfter` 60; `sending.today.pausedUntil` set; `send.paused` webhook delivered with a valid signature |
| Known chats | OK — a private message that arrived during the test recorded the chat twice (phone-number JID and LID) |
| Dashboard (Chrome, pt-BR) | OK — "Hoje: 3 enviadas"; a failed event shows "falhas: 1 · Ver"; the dialog lists it and "Descartar todos" removed it from the database; no script errors |
| Back to the default settings | OK — no daily limit, counter still shown after a restart (a defect found here: before the fix the dashboard showed 0 until the first send of the day) |

## Not covered yet

- `SEND_NEW_CHAT_SCHEDULE` with a real stranger: it would mean messaging someone who never
  talked to the number. Covered by unit tests (`TestNewChatLimitOnlyCountsStrangers`,
  `TestIsNewChatOnlyWhenTheLimitIsOn`); the record of known chats was confirmed live.
- History sync marking the chats of a newly linked number as known: needs a new pairing.
- A full 1 h 40 min retry cycle ending in `failed`: the failed state was set in the database to
  exercise the routes; the transition itself is covered by `TestOutboxRetriesWithTheSameIDThenKeepsTheFailure`.
