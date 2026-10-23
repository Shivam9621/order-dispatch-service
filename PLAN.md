# Order Dispatch Service (Go): Build Plan for Cursor

## How to use this file
Place this file in the repo root as `PLAN.md`. In Cursor, tell it: *"Read PLAN.md. Implement one phase at a time. After each phase, stop, run the listed checks, and wait for me to say 'next'."*

## Rules for Cursor (apply to every phase)
- Go 1.22+. Idiomatic Go only: small packages, explicit error handling, no global mutable state.
- Standard library first. Allowed deps: `chi` (router), `pgx` (Postgres), `testify` (tests), `google/uuid`.
- Always pass `context.Context` as the first parameter to anything that does I/O or blocks.
- Wrap errors with `fmt.Errorf("doing X: %w", err)`.
- Use `log/slog` for structured logging.
- Every phase must compile, pass `go vet ./...` and `go test -race ./...` before finishing.
- Add short comments explaining *why* for concurrency code (channels, mutexes, goroutine lifecycles). I need to be able to explain every line in an interview.
- Do not add features that are not in this plan.

## Project goal
A mini food-delivery backend. Customers place orders through a REST API. Orders are processed in the background by a goroutine worker pool, which assigns the nearest available delivery partner. An LLM-powered assistant endpoint lets users ask about or cancel orders using tool calling.

## Tech stack
- Go, chi, pgx, PostgreSQL (via docker-compose)
- Anthropic Messages API (plain `net/http`, no SDK) for the assistant
- Docker, GitHub Actions (build + test)

## Folder structure
```
order-dispatch/
├── cmd/server/main.go
├── internal/
│   ├── config/          # env loading
│   ├── domain/          # Order, Partner, statuses (pure types)
│   ├── store/           # Postgres repo + interfaces
│   ├── api/             # HTTP handlers, routes, middleware
│   ├── worker/          # worker pool, job queue
│   ├── dispatch/        # partner assignment logic
│   └── assistant/       # LLM client + tool definitions
├── migrations/
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── README.md
└── PLAN.md
```

---

## Phase 1: Skeleton and config
**Build**
- `go mod init github.com/<user>/order-dispatch`
- `cmd/server/main.go` starting an HTTP server on `PORT` (default 8080) with `GET /healthz` returning `{"status":"ok"}`.
- `internal/config` loading env vars: `PORT`, `DATABASE_URL`, `WORKER_COUNT` (default 4), `LLM_API_KEY`, `LLM_MODEL`.
- Makefile targets: `run`, `test`, `lint`, `up` (docker compose up -d).
- `docker-compose.yml` with Postgres 16.

**Check:** `curl localhost:8080/healthz` returns ok.

## Phase 2: Domain and storage
**Domain types** (`internal/domain`)
- `Order`: ID (uuid), CustomerID, Items (JSON), TotalPaise (int64), Lat, Lng, Status, PartnerID (nullable), CreatedAt, UpdatedAt.
- `OrderStatus`: `PLACED`, `CONFIRMED`, `ASSIGNED`, `DELIVERED`, `CANCELLED`. Add a method `CanTransitionTo(next)` enforcing valid transitions only.
- `Partner`: ID, Name, Lat, Lng, Available (bool).

**Migrations** (`migrations/`): tables `orders` and `partners`, plus a seed file with ~10 partners at different Bangalore coordinates.

**Store** (`internal/store`)
- Define a `Store` interface (CreateOrder, GetOrder, ListOrders, UpdateOrderStatus, ListAvailablePartners, MarkPartnerBusy, MarkPartnerFree).
- Implement it with pgx and a connection pool.
- `MarkPartnerBusy` must be atomic: `UPDATE partners SET available=false WHERE id=$1 AND available=true`, returning whether a row was updated. This prevents two orders claiming one partner.

**Tests:** table-driven tests for `CanTransitionTo`. Integration tests for the store can use the docker Postgres.

## Phase 3: REST API
Routes (chi):
- `POST /orders` body: `{customer_id, items:[{name, qty, price_paise}], lat, lng}`. Validate input, compute total, save with status `PLACED`, enqueue for processing, return `201` with the order.
- `GET /orders/{id}`
- `GET /orders?customer_id=...&limit=...`
- `POST /orders/{id}/cancel` (only allowed from `PLACED`, `CONFIRMED`, or `ASSIGNED`; free the partner if assigned).

Also add:
- Middleware: request ID, structured request logging, panic recovery, per-request timeout via `context.WithTimeout` (5s).
- Consistent JSON error format: `{"error":"message"}` with correct status codes.
- Handler tests with `httptest` and a fake in-memory `Store`.

## Phase 4: Worker pool (core Go concurrency piece)
Build `internal/worker`:
- A `Pool` with `N` worker goroutines reading from a buffered `chan OrderJob`.
- `Submit(job)` is non-blocking; if the queue is full, return `ErrQueueFull` and the API responds `503`.
- Each worker processes an order through the pipeline: `PLACED -> CONFIRMED` (simulate 1-2s of work with `time.Sleep`) `-> ASSIGNED` (call the dispatcher) `-> DELIVERED` (simulate 5-10s).
- Respect context cancellation at each step. If an order is cancelled mid-processing, stop and free the partner.
- **Graceful shutdown:** `Pool.Shutdown(ctx)` stops accepting jobs, lets in-flight jobs finish (bounded by ctx timeout), and uses `sync.WaitGroup`.
- On server start, re-enqueue any orders still in `PLACED` or `CONFIRMED` (crash recovery).

**Tests:** submit many jobs concurrently, assert all reach `DELIVERED`, no goroutine leaks, and `go test -race` is clean.

## Phase 5: Partner dispatch
Build `internal/dispatch`:
- Fetch available partners, compute distance to the order using the Haversine formula, pick the nearest.
- Try to claim with the atomic `MarkPartnerBusy`. If the claim fails (someone else took them), try the next nearest.
- If no partner is available, retry with backoff (up to a limit), then mark the order `CANCELLED` with a reason.
- Free the partner on `DELIVERED` or `CANCELLED`.

**Tests:** unit-test Haversine and nearest selection. Add a concurrency test: 50 orders, 5 partners, assert no partner is ever assigned to two active orders at once.

## Phase 6: AI assistant (agentic feature)
Build `internal/assistant` and `POST /assistant` with body `{customer_id, message}`.

**LLM client**
- Plain `net/http` call to `https://api.anthropic.com/v1/messages` with headers `x-api-key`, `anthropic-version: 2023-06-01`, and `content-type: application/json`. Model from `LLM_MODEL` env var.
- Use the 10s context timeout.

**Tools to expose** (JSON schema definitions):
1. `get_order_status(order_id)`
2. `list_recent_orders(customer_id, limit)`
3. `cancel_order(order_id)`

**Agent loop**
1. Send the user message plus tool definitions.
2. If the response has `stop_reason: "tool_use"`, run the matching Go function, append a `tool_result` message, and call the API again.
3. Repeat until a final text answer, with a **max of 5 iterations** as a safety cap.
4. **Security:** every tool must verify the order belongs to `customer_id`. Never trust IDs from the model alone.
5. Return the final text to the user.

**Tests:** mock the LLM HTTP server with `httptest` and test the tool-call loop, the iteration cap, and the ownership check.

## Phase 7: Polish for the interview
- **Graceful shutdown in main:** handle `SIGINT`/`SIGTERM`, call `http.Server.Shutdown`, then `Pool.Shutdown`.
- **Metrics (optional):** simple `GET /metrics` JSON with orders processed, queue depth, active workers (use `sync/atomic`).
- **Dockerfile:** multi-stage build, final image on `gcr.io/distroless/static`.
- **GitHub Actions:** run `go vet`, `go test -race ./...`, and build on every push.
- **README.md** containing:
  - One-paragraph summary and an architecture diagram (Mermaid is fine)
  - How to run (`make up && make run`)
  - Example `curl` commands for every endpoint
  - A "Design decisions" section: why a worker pool, why atomic partner claim, how shutdown works, what happens on crash, known limitations and what you would do next (e.g., a real queue like Kafka or SQS, idempotency keys, retries with dead-letter handling)

---

## Definition of done
- [x] `make up && make run` works from a clean clone
- [x] `go test -race ./...` passes
- [x] Placing 100 orders concurrently ends with all `DELIVERED` and no double-assigned partners
- [x] Ctrl+C shuts down cleanly without losing in-flight orders
- [x] The assistant can check status and cancel an order via natural language
- [x] README has a diagram, curl examples, and design decisions
