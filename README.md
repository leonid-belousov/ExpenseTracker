# Family expense tracker

A family expense-tracking service:

- **Telegram bot** with an LLM-based parser. Family members write in plain language ("кофе 350", "такси 1200 вчера", "сколько потратил на еду в этом месяце", "покажи траты за неделю", "последнее было не 350, а 450", "удали такси"). Each member can only see and change their own expenses.
- **Web dashboard** for the head of the family. It lists, filters (member, period, category), creates, edits and deletes expenses for every configured family member. There is no login, as the brief requires (see [Security](#security-and-exposure)).
- **Go backend**: a REST API, a PostgreSQL store with versioned migrations, and the bot worker.

The repository holds two independent projects that are combined by the root `docker-compose.yml`:

```
ExpenseTracker/
├── backend/            Go module: REST API, Telegram bot, migrations (own Dockerfile)
├── frontend/           React + TypeScript + Vite dashboard (own package.json, Dockerfile, nginx.conf)
├── docker-compose.yml  db + migrate + api + bot (from backend/) + frontend
├── .env.example        settings for the whole stack
└── README.md
```

Each project builds, tests and runs on its own. Everything starts together with one Docker Compose command.

> This is an MVP built for a test assignment. It is not production-hardened (see [Known limitations](#known-limitations)).

## Quick start

Requirements: Docker with Compose v2.

```bash
cp .env.example .env
# edit .env: set POSTGRES_PASSWORD, FAMILY_MEMBERS, TELEGRAM_BOT_TOKEN, LLM_PROVIDER (+ LLM_API_KEY)
docker compose up --build
```

- Dashboard: <http://127.0.0.1:8080>
- REST API: <http://127.0.0.1:8081/api/expenses> (also reachable through the dashboard at `/api/...`)

Startup order is enforced by health checks, not by timing:
`db` (healthy) → `migrate` (one-shot, must exit 0) → `api` (healthy) and `bot` → `frontend`.
Database data lives in the named volume `pgdata`. `docker compose down -v` deletes it.

If a required setting is missing, the affected service exits with a message that lists every problem, for example:

```
error: invalid configuration:
  - TELEGRAM_BOT_TOKEN is required (create a bot with @BotFather)
  - LLM_PROVIDER is required: anthropic, openai or mock (mock is for local development only)
```

To try the dashboard without a Telegram bot: `docker compose up --build db migrate api frontend`. (When started without its settings, the `bot` container keeps restarting and prints the configuration error above.)

If the image build fails at `go mod download` because `proxy.golang.org` is unreachable from your network, set `GOPROXY=https://goproxy.io,direct` (or another mirror) in `.env`. `go.sum` still pins every module hash.

## Architecture

```
Telegram ──long polling──▶ bot ─┐                        ┌─▶ PostgreSQL
                                ├─▶ expense.Service ─────┤   (expenses, telegram_updates,
browser ─▶ frontend (nginx) ─/api─▶ api ┘  validation + scope  │    schema_migrations)
                                                         │
                     bot ──▶ LLM (Anthropic / OpenAI-compatible / mock) — parser only
```

### Backend (`backend/`)

All backend services run one Go binary (`backend/cmd/expense-tracker`) with subcommands: `migrate`, `api`, `bot`, `healthcheck`. Paths below are relative to `backend/`.

| Package | Responsibility |
|---|---|
| `internal/money` | Exact amounts as `int64` minor units. Parsing and formatting never use floats. |
| `internal/dates` | Calendar `Date`, half-open `Range`, period presets (Monday-based weeks). |
| `internal/expense` | Domain model, closed category set, validation, `Service`, `Store` port, owner `Scope`. |
| `internal/store/postgres` | pgx store, embedded SQL migrations with an advisory lock. |
| `internal/api` | REST handlers, error format, CORS/origin policy. |
| `internal/llm` | Prompt, JSON schema, strict intent decoder, Anthropic / OpenAI-compatible / mock backends. |
| `internal/bot` | Update handling, authorisation, idempotency, intent execution, ru/en replies. |
| `internal/telegram` | Minimal Bot API client (getUpdates / sendMessage). Errors are scrubbed of the token. |

### Frontend (`frontend/`)

React 19 + TypeScript, built with Vite. No UI or state libraries: plain components and hooks.

| File | Responsibility |
|---|---|
| `src/App.tsx` | Page state: config, filters, pagination, list loading (superseded requests are aborted), create/edit/delete flow. |
| `src/components/FiltersBar.tsx` | Member / category / date filters and presets (this week, this month, ...). |
| `src/components/ExpenseTable.tsx` | Table that becomes cards at phone width. |
| `src/components/ExpenseDialog.tsx` | Create/edit form in a modal `<dialog>`, with client and API field errors. |
| `src/api.ts` | Typed REST client and `ApiError`. Converts the inclusive "по дату" into the API's exclusive `to`. |
| `src/dates.ts`, `src/format.ts` | Calendar-date arithmetic in UTC (Monday weeks), exact money formatting without floats, form validation, category labels. |
| `nginx.conf` | Serves the build, proxies `/api` to the `api` service, sends security headers (CSP etc.). |

The browser talks only to its own origin (`/api` is proxied), so CORS is not needed in normal use. The dashboard shows a list or table only after the API confirms a load, and closes the edit dialog only after the API confirms the save.

### Ownership and authorisation model

- **Bot actor:** the Telegram user id comes only from the verified update (`message.from.id`). The bot serves only ids listed in `FAMILY_MEMBERS`, and only in private chats. Anyone else gets "access denied" plus their own id, never family data. The LLM is not called for them.
- **Scope in SQL:** every read and write of existing rows takes an `expense.Scope`, enforced in the `WHERE` clause (`user_id = ANY($scope)`). The bot always uses `OwnerScope(actor)`. The dashboard uses the scope of all configured family members.
- **The LLM is an untrusted parser.** It returns one JSON object with a fixed schema. Go validates every field (action whitelist, canonical category, exact amount, date) and ignores extra fields such as `user_id`. The model cannot name an owner, run queries, or call other operations.
- **Dashboard-created expenses** must name a `user_id` that is a configured member. The API rejects anything else (`400`). It never creates members implicitly.

### Idempotency

The bot uses long polling and processes updates one at a time. For each update, it claims `update_id` in the `telegram_updates` table **in the same transaction** as the operation:

- A redelivered update finds the claim and is skipped. There is also a pre-check, so the LLM isn't called again.
- If the operation fails, the claim is rolled back with it, and a later retry is processed normally.
- The reply is sent only **after commit**, so the bot never reports success for something that wasn't saved.

## Domain rules and assumptions

| Topic | Rule |
|---|---|
| Deployment | One family per deployment. Members and the bot allowlist come from `FAMILY_MEMBERS=<telegram_id>:<name>,...`. |
| Categories | Closed set, stored and sent by the API as `food, transport, housing, entertainment, health, shopping, other`. The UI and bot show localized labels (еда, транспорт, жильё, развлечения, здоровье, покупки, другое). |
| Currency | One ISO code per deployment (`CURRENCY`, default `RUB`). It is shown in every bot summary and in the dashboard. |
| Amounts | `NUMERIC(14,2)` in the DB and `int64` minor units in Go, always > 0 and ≤ 999 999 999.99. At most 2 decimal places; **no rounding**: `3.555` is rejected. `,` and `.` are both accepted as the decimal separator. The API returns amounts as strings (`"350.50"`) and accepts a string or a JSON number literal (parsed from its text, never through float). Sums are computed with exact `NUMERIC` in SQL. |
| Dates | `spent_at` is a `DATE`. `created_at` / `updated_at` are `timestamptz`, returned in UTC. "Today" / "yesterday" are computed in `APP_TIMEZONE`. `spent_at` must be between 2000-01-01 and today, inclusive (no future expenses). |
| Periods | Half-open everywhere: `[from, to)`. Weeks run Monday to Sunday. The API's `to` is **exclusive**. The dashboard shows an inclusive "по дату" field and sends the next day. The bot's default period for lists and summaries is the current month. "За неделю" means the current calendar week. |
| Comments | Optional. Trimmed, with control characters removed, max 500 characters. An empty comment is stored as `NULL`. |
| Category inference | The LLM maps clear everyday items (coffee→food, taxi→transport, ...). If unclear, it returns `null`, and the bot asks for the category without saving anything. |
| Update/delete targeting | The target must resolve to **exactly one** of the actor's expenses: <br>• "The last one" is the most recently *recorded* expense. If it doesn't match the stated old value (e.g. "not 350" but the last is 1200), the bot asks instead of guessing. <br>• Otherwise the bot matches on amount, category, a comment word and date (AND). No match → "not found". Several matches → the bot lists up to 5 (date · amount · category · comment) and asks to specify. **Nothing is mutated in either case.** <br>• Bulk deletes aren't supported. |
| Conversation state | None. Every message is processed on its own. After a clarification question, the user resends the request with more detail (e.g. "удали такси 800"). |

## REST API

Base path `/api`. Every request and response is JSON. Mutating requests must send `Content-Type: application/json`.

| Method | Path | Description |
|---|---|---|
| GET | `/api/config` | Currency, time zone, today's date, categories, members |
| GET | `/api/expenses` | List with filters (see below) |
| POST | `/api/expenses` | Create |
| GET | `/api/expenses/{id}` | Get one |
| PUT | `/api/expenses/{id}` | Replace editable fields |
| DELETE | `/api/expenses/{id}` | Delete (`204`) |
| GET | `/healthz`, `/api/health` | Liveness + DB ping |

**List query parameters:** `user_id`, `category`, `from` (inclusive, `YYYY-MM-DD`), `to` (exclusive), `limit` (1–200, default 50), `offset`. Unknown parameters are rejected. Results are sorted by `spent_at DESC, id DESC`. The response includes `total` and the exact `sum` for **the whole filtered set**, not just the current page:

```json
{"items":[{"id":1,"user_id":111,"member_name":"Мама","amount":"350.00","category":"food",
  "spent_at":"2026-10-15","comment":"кофе","created_at":"2026-10-15T09:00:00Z","updated_at":"2026-10-15T09:00:00Z"}],
 "total":1,"sum":"350.00","currency":"RUB","limit":50,"offset":0}
```

**Create / update body:** `{"user_id":111,"amount":"350.00","category":"food","spent_at":"2026-10-15","comment":"кофе"}`. `comment` is optional, and `null` clears it. Unknown fields are rejected.

**Errors** use a stable shape:

```json
{"error":{"code":"validation_error","message":"request validation failed","fields":{"amount":"amount must be greater than zero"}}}
```

| Status | `code` |
|---|---|
| 400 | `validation_error`, `invalid_body` |
| 403 | `origin_not_allowed` |
| 404 | `not_found` |
| 413 | `body_too_large` |
| 415 | `unsupported_media_type` |
| 503 | `unavailable` (storage failure or timeout; nothing was confirmed) |

## Configuration

Every setting is documented in [`.env.example`](.env.example). The required ones:

| Variable | Used by | Notes |
|---|---|---|
| `POSTGRES_PASSWORD` | db, all | Use URL-safe characters. Compose refuses to start without it. |
| `FAMILY_MEMBERS` | api, bot | `111111111:Мама,222222222:Папа` |
| `TELEGRAM_BOT_TOKEN` | bot | From @BotFather. Validated at startup with `getMe`. |
| `LLM_PROVIDER` | bot | `anthropic`, `openai` or `mock` |
| `LLM_API_KEY` | bot | Required for `anthropic` / `openai` |

Optional: `APP_TIMEZONE` (default `Europe/Moscow`), `CURRENCY` (`RUB`), `BOT_LANGUAGE` (`ru`/`en`), `LLM_MODEL`, `LLM_BASE_URL`, `LLM_TIMEOUT` (`30s`), `WEB_PORT` (`8080`), `API_PORT` (`8081`), `CORS_ALLOWED_ORIGINS`, `LOG_LEVEL`.

### Telegram bot setup

1. Create a bot with [@BotFather](https://t.me/BotFather) and put the token in `TELEGRAM_BOT_TOKEN`.
2. Find each family member's numeric Telegram id. Either use @userinfobot, or start the stack and message the bot: non-members get a reply with their own id.
3. List the members in `FAMILY_MEMBERS` and restart: `docker compose up -d bot api`.

The bot uses long polling, so it needs no public URL or webhook. If a webhook is set for this token, Telegram answers `409` and the bot logs it. Remove the webhook with `https://api.telegram.org/bot<token>/deleteWebhook`. Run only one bot instance per token.

### LLM provider setup

- **Anthropic (Claude):** `LLM_PROVIDER=anthropic`, `LLM_API_KEY=sk-ant-...`. `LLM_MODEL` defaults to `claude-opus-5-5`. A cheaper model such as `claude-haiku-5-5` works for this simple parsing task if you prefer. The bot uses the official Go SDK with schema-constrained JSON output and low effort.
- **OpenAI-compatible:** `LLM_PROVIDER=openai`, `LLM_BASE_URL` (default `https://api.openai.com/v1`), `LLM_MODEL`, `LLM_API_KEY`. Works with OpenAI, OpenRouter, vLLM, or Ollama (`http://host.docker.internal:11434/v1`, any non-empty key). The bot uses `response_format: json_object`.
- **Mock (development only):** `LLM_PROVIDER=mock`. A deterministic rule-based parser that understands roughly the brief's examples in Russian and English. It logs a warning at startup. **Do not use it for real families:** it misreads anything outside those patterns.

Every backend's output goes through the same strict decoder. On a timeout, provider error, refusal, or malformed answer, the user is told that nothing was changed.

## Security and exposure

- **The dashboard and API have no authentication**, as the brief requires. Anyone who can reach them can read and change every family expense.
- Compose therefore publishes them **only on `127.0.0.1`** (`127.0.0.1:8080` and `127.0.0.1:8081`). The database isn't published at all.
- **Do not expose these ports publicly.** If remote access is needed, put the `frontend` service behind a trusted access-control layer: a VPN or Tailscale, an SSH tunnel, or a reverse proxy with authentication (e.g. oauth2-proxy, Cloudflare Access, basic auth over TLS). Change the port bindings only together with that layer. This project doesn't make an unauthenticated public deployment safe.
- **CORS** is an explicit origin list (`CORS_ALLOWED_ORIGINS`), and wildcards are rejected. Mutating requests must be `application/json` and are refused (`403`) when they carry a foreign `Origin`. This blocks drive-by cross-site writes from other web pages you visit. It does not replace authentication.
- nginx sends a strict CSP, `X-Frame-Options: DENY` and `nosniff`. The UI renders data with `textContent` only.
- **Logs** contain no tokens, API keys, message text, or expense details. They contain only update ids, actions, statuses and error classes. The Telegram client scrubs the token from network errors.

## Development

Requirements: Go 1.26+, Node.js 22+, Docker (for integration tests and Compose).

### Backend

```bash
cd backend
go build ./...
gofmt -l .                 # must print nothing
go vet ./...
go test ./...              # unit tests (memstore, fakes for Telegram and the LLM)
```

PostgreSQL integration tests run when `TEST_DATABASE_URL` is set. They drop and recreate their tables, so use a throwaway database:

```bash
docker run -d --rm --name et-testdb -e POSTGRES_USER=expenses -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=expenses_test -p 127.0.0.1:55432:5432 postgres:17-alpine
TEST_DATABASE_URL="postgres://expenses:testpass@127.0.0.1:55432/expenses_test?sslmode=disable" go test ./internal/store/postgres/
docker rm -f et-testdb
```

On Windows, if Docker can't bind the port, it may be in a reserved range. Use `-p 127.0.0.1::5432` and read the assigned port with `docker port et-testdb 5432`.

### Frontend

```bash
cd frontend
npm ci
npm test                   # Vitest: dates, money formatting, validation, API client errors
npm run build              # tsc type check + Vite production build into dist/
npm run dev                # http://127.0.0.1:5173, proxies /api to the backend
```

`npm run dev` proxies `/api` to `http://127.0.0.1:8081` (the API port Compose publishes). Run the backend first, e.g. `docker compose up -d db migrate api`, or point `API_PROXY_TARGET` elsewhere. For dev-server writes, add `http://127.0.0.1:5173` to `CORS_ALLOWED_ORIGINS`: the dev proxy forwards the browser's `Origin` header, and the API refuses writes from unlisted origins.

### Backend test coverage

Test coverage includes ownership isolation between users, non-allowlisted users and group chats, misleading LLM output (an injected `user_id`), amount/category/date validation, half-open range and Monday-week boundaries, consistency between bot summaries and the dashboard filter, ambiguous update/delete with no mutation, CRUD, repeated `update_id` (no duplicate, no second LLM call), concurrent claims, LLM malformed output/timeout/provider errors, storage failure rollback, REST error codes, CORS/origin policy, and token scrubbing.

### Migrations

SQL files in `backend/internal/store/postgres/migrations/NNNN_name.sql` are embedded in the binary. `expense-tracker migrate` (the `migrate` Compose service) applies the pending ones in order. Each file runs in its own transaction, under a PostgreSQL advisory lock, and is recorded in `schema_migrations`. Rerunning is a no-op. Migrations are forward-only: add a new file for every schema change and never edit an applied one.

### Adding a bot language

Add a catalogue to `catalogs` in `backend/internal/bot/messages.go` and allow the code in `config.LoadBot`. Canonical categories and the API don't change.

## Known limitations

- The dashboard has no authentication (by design), and there is no audit log of who changed what in the dashboard.
- One currency per deployment. There is no currency conversion and no per-expense currency.
- The bot has no conversation memory. A clarification question needs the user to resend the full request with more detail.
- Bot updates are processed sequentially by one instance, which is plenty for a family but not horizontally scalable.
- Expenses dated in the future are rejected. Expenses of members removed from `FAMILY_MEMBERS` stay in the database but are hidden from the dashboard and the bot.
- LLM quality depends on the chosen model. The mock parser covers only the example phrasings.
- No HTTPS termination is included. Loopback-only exposure is assumed.
