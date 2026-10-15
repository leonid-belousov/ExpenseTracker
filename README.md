# Family expense tracker

A family expense-tracking service:

- **Telegram bot** with an AI agent (tool calling + short conversation memory). Family members write in plain language ("кофе 350", "такси 1200 вчера", "сколько потратил на еду в этом месяце", "покажи траты за неделю", "последнее было не 350, а 450", "удали такси"). Each member can only see and change their own expenses.
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

**1. Start everything — no configuration needed:**

```bash
docker compose up --build
```

- Dashboard: <http://127.0.0.1:8080>
- REST API: <http://127.0.0.1:8081/api/expenses> (also reachable through the dashboard at `/api/...`)

Without a `.env` file every setting has a local-development default: a dev database password, two demo family members (`111111111:Мама`, `222222222:Папа`), and an **idle bot**. The bot container starts, stays healthy, and logs once that `TELEGRAM_BOT_TOKEN` is not set; it never crash-loops.

**2. Turn on the Telegram bot:**

```bash
cp .env.example .env
# edit .env: TELEGRAM_BOT_TOKEN, FAMILY_MEMBERS (your real Telegram ids),
# LLM_PROVIDER + LLM_API_KEY (or LLM_PROVIDER=mock to try it offline)
docker compose up -d
```

No API key at hand? Run a local model in the optional `local-llm` profile:

```bash
docker compose --profile local-llm up -d
docker compose exec ollama ollama pull qwen2.5:7b
# .env: LLM_PROVIDER=openai  LLM_BASE_URL=http://ollama:11434/v1  LLM_MODEL=qwen2.5:7b  LLM_API_KEY=ollama
docker compose up -d bot
```

Small local models handle tool calling noticeably worse than Claude; measure with the [eval](#agent-eval).

Startup order is enforced by health checks, not by timing:
`db` (healthy) → `migrate` (one-shot, must exit 0) → `api` (healthy) and `bot` → `frontend`.
Database data lives in the named volume `pgdata`. `docker compose down -v` deletes it.

Once `TELEGRAM_BOT_TOKEN` is set, the bot validates the rest of its configuration and exits with a message that lists every problem, for example:

```
error: invalid configuration:
  - LLM_PROVIDER is required: anthropic, openai or mock (mock is for local development only)
```

**Database password.** The default `expenses-dev` is for local use only. For anything else set `POSTGRES_PASSWORD` in `.env` **before the first start**: PostgreSQL stores the password when it initialises the `pgdata` volume, so changing it later requires `docker compose down -v` (which deletes the data) or `ALTER USER` inside the database.

If the image build fails at `go mod download` because `proxy.golang.org` is unreachable from your network, set `GOPROXY=https://goproxy.io,direct` (or another mirror) in `.env`. `go.sum` still pins every module hash.

## Architecture

```
Telegram ──long polling──▶ bot ──▶ agent ──tools──┐                    ┌─▶ PostgreSQL
                                     │            ├─▶ expense.Service ─┤   (expenses, chat_messages,
browser ─▶ frontend (nginx) ─/api─▶ api ──────────┘  validation+scope  │    telegram_updates, ...)
                                     │
                                     └──▶ LLM (Anthropic / OpenAI-compatible / offline mock)
```

### Backend (`backend/`)

All backend services run one Go binary (`backend/cmd/expense-tracker`) with subcommands: `migrate`, `api`, `bot`, `healthcheck`. A second binary, `backend/cmd/eval`, runs the agent eval. Paths below are relative to `backend/`.

| Package | Responsibility |
|---|---|
| `internal/money` | Exact amounts as `int64` minor units. Parsing and formatting never use floats. |
| `internal/dates` | Calendar `Date`, half-open `Range`, period presets (Monday-based weeks). |
| `internal/expense` | Domain model, closed category set, validation, `Service`, `Store` port, owner `Scope`. |
| `internal/store/postgres` | pgx store, embedded SQL migrations with an advisory lock. |
| `internal/api` | REST handlers, error format, CORS/origin policy. |
| `internal/llm` | Provider-neutral tool-calling interface; Anthropic (official SDK, strict tools) and OpenAI-compatible (function calling) adapters. |
| `internal/agent` | The AI agent: system prompt, tools and their execution in the sender's scope, the tool-use loop, the conversation window, the offline `RuleModel`. |
| `internal/bot` | Update handling, authorisation, idempotency, memory, one transaction per message, receipts, ru/en fixed texts. |
| `internal/eval` + `cmd/eval` | Agent quality eval: dataset runner, deterministic grading, report (see [Agent eval](#agent-eval)). |
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
| `src/dates.ts`, `src/format.ts` | Calendar-date arithmetic in UTC (Monday weeks), locale-aware exact money and date formatting without floats, form validation (returns language-neutral codes). |
| `src/i18n/` | Typed dictionaries (`messages.ts`), `I18nProvider` / `useI18n`, persistence of the chosen language. |
| `src/components/LanguageSwitcher.tsx` | RU / EN switch in the header. |
| `nginx.conf` | Serves the build, proxies `/api` to the `api` service, sends security headers (CSP etc.). |

#### Languages (i18n)

The dashboard supports **Russian (default)** and **English**. The RU / EN switch is in the header. The choice is saved in the browser's `localStorage` (`expense-tracker.locale`); if storage is unavailable or holds an unknown value, Russian is used. Switching changes all texts, category labels, number and date formats (`1 200,50 RUB` / `15.10.2026` vs `1,200.50 RUB` / `Oct 15, 2026`), the page `<title>` and `<html lang>`. Errors are stored as codes and translated at render time, so messages already on screen switch too.

There is no i18n library: dictionaries are plain typed objects. `ru` is the reference, and `en` is typed as `Messages`, so a missing key or a wrong parameter list is a compile error; a test also checks that both dictionaries have the same keys and that English has no Cyrillic left. Parameterised messages are functions, e.g. `page: (page, pages) => ...`. To add a language, add its code to `LOCALES`, a dictionary typed as `Messages`, and an entry in `dictionaries` and in `LanguageSwitcher`.

API data is not translated: the backend returns canonical category keys (translated in the UI) and English error texts. The UI shows its own localised message per error kind and per rejected field instead of the raw server text. The Telegram bot's language is configured separately (`BOT_LANGUAGE`).

The browser talks only to its own origin (`/api` is proxied), so CORS is not needed in normal use. The dashboard shows a list or table only after the API confirms a load, and closes the edit dialog only after the API confirms the save.

### AI agent

The bot is a tool-calling agent (`internal/agent`). For every message the model gets a fixed system prompt, five tools, the recent conversation and the new message, and works in a loop: it calls tools, reads their JSON results, and finally answers in text.

| Tool | What it does |
|---|---|
| `add_expenses` | Records 1–10 expenses at once ("кофе 350, булка 120 и такси 600"). All items are validated before anything is saved. |
| `find_expenses` | Filters by period (preset or dates), category, exact amount, comment substring; sorts by date or by recording time ("the last one"); returns ids, total count and exact sum. |
| `summarize_expenses` | Total and count for a period (default: this month) with a per-category breakdown. |
| `update_expense` | Changes amount / category / date / comment of one expense by id. |
| `delete_expense` | Deletes one expense by id. |

The tool schemas are **strict** (every property required, `additionalProperties: false`, nullable optionals), so both providers produce schema-valid arguments. Go still validates every value (exact money, canonical category, date range) and returns problems to the model as `is_error` tool results, which it explains to the user ("сумма должна быть больше нуля").

How the agent behaves (encoded in the system prompt, measured by the eval):

- Infers the category itself; when nothing fits it records `other` and says so, instead of refusing to save.
- Before changing or deleting, it looks the expense up and acts only when exactly one matches; with several matches it lists them and asks. If the user states an old value the expense doesn't have ("последнее было не 350" but the last one is 1200), it asks instead of changing.
- Answers questions with the tools' exact numbers and names the period.
- Replies in the user's language, briefly, without Markdown.

**Receipts.** After the model's text the bot appends one line per change — `Записал: …`, `Исправил: … → …`, `Удалил: …` — generated from what the tools actually did, never from the model's words. No receipt means nothing was changed.

**Conversation memory.** The last messages of each member (default: up to 12 messages from the last 6 hours, at most 6 000 characters) are stored in `chat_messages` and given to the model as history, so a clarification can be answered briefly: "удали такси" → "Нашёл две…" → "ту, что за 800". Stored bot replies carry a short note with the expense ids they showed (`[ids: 12, 15]`); this is how the agent can act on "ту, что за 800" without searching again. The window is trimmed from the oldest side to the character budget, always starts with a user message and alternates roles. Memory is per member and is deleted after 7 days.

**Provider adapters** (`internal/llm`) keep each provider's native transcript append-only within a turn: the assistant's answer (including Claude's thinking blocks) is sent back verbatim, followed by all tool results in one message. Earlier turns are passed as plain text, never as replayed provider blocks. This keeps Claude's preserved-thinking check satisfied and the prompt cache (tools + system prompt, both byte-stable) warm. On the Claude API with a model that supports it, the server-side refusal fallback (`fallbacks: "default"`) is enabled.

### Ownership, authorisation and failure model

- **Bot actor:** the Telegram user id comes only from the verified update (`message.from.id`). The bot serves only ids listed in `FAMILY_MEMBERS`, and only in private chats. Anyone else gets "access denied" plus their own id, never family data. The LLM is not called for them.
- **Scope in SQL:** every read and write of existing rows takes an `expense.Scope`, enforced in the `WHERE` clause (`user_id = ANY($scope)`). The bot always uses `OwnerScope(actor)`. The dashboard uses the scope of all configured family members.
- **The model is untrusted.** No tool takes an owner: tools always act on the sender's expenses, and injected arguments such as `user_id` are ignored. `update_expense` / `delete_expense` accept only ids the model has seen in a tool result (this turn or the noted ids of recent replies), so a hallucinated id is rejected; an id of another member is simply not found. One message can make at most 20 changes, and the loop is capped at `AGENT_MAX_STEPS` model calls.
- **One transaction per message.** The bot claims the Telegram `update_id`, runs the whole agent loop, and writes the memory in **one** database transaction. If the model times out, fails, refuses, exceeds the step limit, or the storage fails at any step, everything is rolled back — including changes made by earlier tool calls in that message — and the user is told that nothing was changed. The trade-off: the transaction stays open during model calls. That is fine for a family-sized bot that handles one update at a time; it would not be for a high-traffic service.
- **Idempotency.** A redelivered update finds its claim and is skipped (a pre-check also avoids calling the model again). A failed update leaves no claim, so Telegram's retry is processed normally. The reply is sent only **after commit**.
- **Dashboard-created expenses** must name a `user_id` that is a configured member. The API rejects anything else (`400`). It never creates members implicitly.

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
| Category inference | The agent maps everyday items (coffee→food, taxi→transport, ...). If nothing fits, it records `other` and says so; the user can correct it in the next message. |
| Update/delete targeting | The agent finds the expense first and changes it only if **exactly one** matches. "The last one" is the most recently *recorded* expense; if it doesn't match a stated old value (e.g. "not 350" but the last is 1200), the agent asks. With several matches it lists them and asks. Several expenses are changed or deleted only on an explicit request ("удали все траты за сегодня"), at most 20 per message. |
| Conversation state | Short memory per member: recent messages (6 h window, 12 messages, 6 000 characters), deleted after 7 days. A clarification can be answered briefly ("ту, что за 800"). |

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

Every setting is documented in [`.env.example`](.env.example). Nothing is required to start the stack; these are the ones to set for real use:

| Variable | Used by | Notes |
|---|---|---|
| `POSTGRES_PASSWORD` | db, all | Default `expenses-dev` (local use only). Use URL-safe characters. Set it before the first start (see above). |
| `FAMILY_MEMBERS` | api, bot | Default: two demo members. Format `111111111:Мама,222222222:Папа` with real Telegram ids. |
| `TELEGRAM_BOT_TOKEN` | bot | From @BotFather. Without it the bot stays idle; with it, it is validated at startup with `getMe`. |
| `LLM_PROVIDER` | bot | `anthropic`, `openai` or `mock`. Required once the token is set. |
| `LLM_API_KEY` | bot | Required for `anthropic` / `openai` |

Optional: `LLM_WORKSPACE_ID` (Anthropic keys not scoped to a workspace), `APP_TIMEZONE` (default `Europe/Moscow`), `CURRENCY` (`RUB`), `BOT_LANGUAGE` (`ru`/`en`, the default reply language and the language of fixed texts), `LLM_MODEL`, `LLM_BASE_URL`, `LLM_TIMEOUT` (`30s`, per model call), `AGENT_MAX_STEPS` (`6`), `WEB_PORT` (`8080`), `API_PORT` (`8081`), `CORS_ALLOWED_ORIGINS`, `LOG_LEVEL`.

### Telegram bot setup

1. Create a bot with [@BotFather](https://t.me/BotFather) and put the token in `TELEGRAM_BOT_TOKEN`.
2. Find each family member's numeric Telegram id. Either use @userinfobot, or start the stack and message the bot: non-members get a reply with their own id.
3. List the members in `FAMILY_MEMBERS` and restart: `docker compose up -d bot api`.

If the bot logs `cannot reach Telegram: ... dial tcp 127.0.0.1:443`, the host's DNS maps `api.telegram.org` to a local unblocking tool, which containers can't reach. Pin the Bot API address for the bot in a git-ignored `docker-compose.override.yml`:

```yaml
services:
  bot:
    extra_hosts:
      - "api.telegram.org:149.154.167.220"
```

The bot uses long polling, so it needs no public URL or webhook. If a webhook is set for this token, Telegram answers `409` and the bot logs it. Remove the webhook with `https://api.telegram.org/bot<token>/deleteWebhook`. Run only one bot instance per token.

### LLM provider setup

- **Anthropic (Claude):** `LLM_PROVIDER=anthropic`, `LLM_API_KEY=sk-ant-...`. `LLM_MODEL` defaults to `claude-opus-5-5`; `claude-sonnet-5-5` scored the same on the [eval](#agent-eval) at about 60% of the cost, and `claude-haiku-5-5` is far cheaper but made two mistakes. The bot uses the official Go SDK with strict client tools, low effort, and a cached tools + system prompt prefix. If the API answers that the key "is not scoped to a workspace", set `LLM_WORKSPACE_ID` (Console → Settings → Workspaces) or create a key inside a workspace.
- **OpenAI-compatible:** `LLM_PROVIDER=openai`, `LLM_BASE_URL` (default `https://api.openai.com/v1`), `LLM_MODEL`, `LLM_API_KEY`. Works with OpenAI, OpenRouter, vLLM, or Ollama (`http://host.docker.internal:11434/v1`, any non-empty key) — the model must support function calling. Tools are sent with `strict: true`.
- **Mock (development only):** `LLM_PROVIDER=mock`. A deterministic rule-based model (`agent.RuleModel`) that drives the same tools for roughly the brief's examples, with no network. It logs a warning at startup. **Do not use it for real families:** it misreads anything outside those patterns (it scores about 61% on the eval).

`LLM_TIMEOUT` bounds one model call; `AGENT_MAX_STEPS` (default 6) bounds model calls per message. On a timeout, provider error, refusal, unusable answer or step limit, the whole message is rolled back and the user is told that nothing was changed.

## Security and exposure

- **The dashboard and API have no authentication**, as the brief requires. Anyone who can reach them can read and change every family expense.
- Compose therefore publishes them **only on `127.0.0.1`** (`127.0.0.1:8080` and `127.0.0.1:8081`). The database isn't published at all.
- **Do not expose these ports publicly.** If remote access is needed, put the `frontend` service behind a trusted access-control layer: a VPN or Tailscale, an SSH tunnel, or a reverse proxy with authentication (e.g. oauth2-proxy, Cloudflare Access, basic auth over TLS). Change the port bindings only together with that layer. This project doesn't make an unauthenticated public deployment safe.
- **CORS** is an explicit origin list (`CORS_ALLOWED_ORIGINS`), and wildcards are rejected. Mutating requests must be `application/json` and are refused (`403`) when they carry a foreign `Origin`. This blocks drive-by cross-site writes from other web pages you visit. It does not replace authentication.
- nginx sends a strict CSP, `X-Frame-Options: DENY` and `nosniff`. The UI renders data with `textContent` only.
- **Logs** contain no tokens, API keys, message text, or expense details. They contain only update ids, tool names, step and token counts, durations, statuses and error classes. The Telegram client scrubs the token from network errors.
- **Message text is stored** in `chat_messages` as the bot's short-term memory and sent to the configured LLM provider. It is pruned after 7 days.

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

Test coverage includes ownership isolation between users, non-allowlisted users and group chats, an adversarial model (another member's id, injected `user_id`, hallucinated ids), strict tool schemas, several expenses in one message, amount/category/date validation, half-open range and Monday-week boundaries, consistency between bot summaries and the dashboard filter, ambiguous update/delete with no mutation, conversation memory (window, per-user isolation, follow-up answers), rollback when the model fails after a tool already changed data, the step limit, the Anthropic and OpenAI wire protocols (append-only transcript, tool results), CRUD, repeated `update_id` (no duplicate, no second model run), concurrent claims, storage failure rollback, REST error codes, CORS/origin policy, and token scrubbing.

### Agent eval

`backend/eval/cases.json` holds 51 scenarios (50 in Russian, one in English), tagged by skill: `create`, `dates`, `amounts`, `category`, `multi`, `read`, `update`, `delete`, `memory` (multi-turn), `security`, `safety`, `offtopic`, `validation`, and `brief` (the brief's own examples). Each case seeds expenses for the sender and for another member, sends one or more messages through the **real** bot pipeline (agent, tools, validation, transaction, memory) on an in-memory store with a fixed date (Thursday 2026-10-15), and is graded by code, not by an LLM:

- the sender's final expenses must equal the expected set (amount, category, date, comment substring) or stay unchanged;
- the other member's expenses must never change — checked in **every** case and reported as `SECURITY`;
- optional checks on the final reply: required numbers/words (digit groups are normalised, so `1 450` matches `1450`) and forbidden text (another member's data, prompt internals).

Run it with the same `LLM_*` variables as the bot (from `backend/`):

```bash
LLM_PROVIDER=mock go run ./cmd/eval                                    # offline baseline, free
LLM_PROVIDER=anthropic LLM_API_KEY=sk-ant-... go run ./cmd/eval -runs 3 -out report.json
LLM_PROVIDER=anthropic LLM_API_KEY=sk-ant-... LLM_MODEL=claude-haiku-5-5 go run ./cmd/eval -tags memory,update
```

It prints every failing case with the replies, a pass-rate table per tag, latency p50/p95 per message, model calls and tokens per message, cache-read share, and the cost (built-in prices for current Claude models, or `-price-in` / `-price-out`). The exit code is non-zero if any case fails. `-runs N` repeats cases because models are not deterministic.

Every case also checks the reply style the system prompt asks for: no memory notes (`[ids: …]`) and no machine-formatted amounts (`1450.00`).

**Results** (2026-10-16, one run each, 56 messages):

| Model | Passed | Latency p50 / p95 | Model calls / message | Input tokens / message (cache reads) | Cost per 1000 messages |
|---|---|---|---|---|---|
| `claude-opus-5-5` (default) | **51 / 51** | 4.0 s / 6.9 s | 1.96 | 7 557 (87%) | $8.53 |
| `claude-sonnet-5-5` | **51 / 51** | 3.2 s / 7.2 s | 2.05 | 7 961 (87%) | $5.28 |
| `claude-haiku-5-5` | 49 / 51 | 4.0 s / 7.2 s | 2.14 | 8 376 (86%) | $0.31 |
| offline `mock` | 31 / 51 | — | — | — | free |

Haiku's two failures were a future-dated expense silently recorded on another date and a weekly list that included the previous week. The mock fails on everything outside the example phrasings (weekdays, "2,5 тыс", follow-ups, injections it can't parse); a Go test keeps it at 100% on the `brief` cases, so the whole eval pipeline runs in `go test ./...` without a network. Single runs are noisy: use `-runs 3` before choosing a model.

What the eval caught while building the agent, and how it was fixed:

- **Every request failed with HTTP 400** on the real API: strict tool use allows at most 16 union-typed (nullable) parameters across all tools, and the first schema had 17. Optional enums now use explicit values (`"any"`, `"unchanged"`, `"none"`), and `TestToolSchemaUnionBudget` keeps the count at 12 or below.
- **Style problems that the state checks don't see**, found by reading the replies: "1450.00 RUB" next to "1 450 ₽", switching between «ты» and «вы» and between «записал» and «записала», a reply that repeated the receipt, the model's memory note `[ids: 3, 2]` copied into a reply, a reply made of a zero-width space, and an English note-to-self ("Future dates are rejected. Ask the user.") before the Russian answer. Fixed with a clearer "Replying" section in the system prompt, `agent.CleanReply` (drops notes, invisible characters, and leading non-Cyrillic paragraphs in Russian replies), and the style checks above.

To add a case, append it to `cases.json` (the loader rejects unknown fields and invalid amounts/dates). Every bug found in real use should become a case.

### Migrations

SQL files in `backend/internal/store/postgres/migrations/NNNN_name.sql` are embedded in the binary. `expense-tracker migrate` (the `migrate` Compose service) applies the pending ones in order. Each file runs in its own transaction, under a PostgreSQL advisory lock, and is recorded in `schema_migrations`. Rerunning is a no-op. Migrations are forward-only: add a new file for every schema change and never edit an applied one.

### Adding a bot language

Add a catalogue to `catalogs` in `backend/internal/bot/messages.go` and allow the code in `config.LoadBot`. Canonical categories and the API don't change.

## Known limitations

- The dashboard has no authentication (by design), and there is no audit log of who changed what in the dashboard.
- One currency per deployment. There is no currency conversion and no per-expense currency.
- The bot's memory is short (hours, a dozen messages); it is not a long-term profile of the user.
- Bot updates are processed sequentially by one instance, and each message holds a database transaction open while the model works. That is plenty for a family but not horizontally scalable.
- The agent's quality depends on the model and is measured, not guaranteed: see the eval. Its replies are free text; only the receipt lines are generated by code.
- Expenses dated in the future are rejected. Expenses of members removed from `FAMILY_MEMBERS` stay in the database but are hidden from the dashboard and the bot.
- The mock model covers only the example phrasings.
- No HTTPS termination is included. Loopback-only exposure is assumed.
