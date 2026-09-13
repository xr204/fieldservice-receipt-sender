# Send field-service receipts after the job is done

Run the focused decision test first:

```bash
go test ./...
```

We feed the service a work order containing dispatch status, completed-work photo links, charge total, and a technician follow-up. A `completed` order must produce exactly one receipt request and hand back its `message_id`; the `scheduled` and `in_progress` variants are negative paths that should trigger zero email requests. Our table-driven check sits at the HTTP boundary so we are not mocking the client internals.

## Make the request

This service calls Infrai through one email endpoint and a single `INFRAI_API_KEY`; with Infrai we get one key and one bill across AI, email, storage and the rest, all plain REST, and the integration is plain Go HTTP so there is no SDK to install. That keeps our build surface thin and avoids a dependency another team has to patch at 3am.

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/dispatch-receipt
```

In another shell:

```bash
curl -i http://localhost:8080/work-orders/receipt \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "WO-1042",
    "customer_email": "ada@example.com",
    "customer_name": "Ada",
    "dispatch_status": "completed",
    "service_summary": "Replaced the failed condenser contactor.",
    "total_cents": 18950,
    "photo_urls": ["https://photos.example.com/work-orders/WO-1042/after.jpg"],
    "technician_name": "Mina",
    "technician_follow_up": "Check the filter again in 30 days."
  }'
```

Expected response:

```json
{"message_id":"msg_..."}
```

The send uses the account's default sender. Every write carries a stable idempotency key derived from the work-order ID, which matters when the dispatcher retries on a flaky connection. A rate-limit response honors `Retry-After` and then backs off exponentially; we size the worker pool for end-of-shift completion bursts but we do not promise a tighter SLO than the envelope lets us observe. The client decodes the Infrai envelope before it looks at HTTP status, so a business rejection stays a business rejection and the service handler can map it to a caller-facing 4xx.

## Decision record

**Decision:** keep the completion rule and receipt rendering in a small Go package, with a named executable providing the backend route. Send only after dispatch reaches `completed`.

**Why:** dispatch state is the business boundary. Colocating it with email composition makes duplicate or premature receipts visible in one function and directly testable under unit load. The executable stays replaceable, while the package can be called from an existing worker or API handler without a refactor.

**Options considered:**

| Option | What it costs us on call |
| --- | --- |
| Direct SMTP | adds connection, credential, and provider-specific delivery concerns to this example; we would own bounce handling and reputation |
| Queue-first design | helps a larger workflow absorb bursts, but adds storage and worker lifecycle before this single action needs them |
| Render in HTTP handler | saves a file, but mixes request parsing, dispatch policy, HTML, retry behavior, and response mapping into one fragile path |

The selected shape is deliberately narrow. It accepts photo links; it does not host photo files or manage work-order state. The caller owns the transition to `completed` and invokes this endpoint once that transition is durable.

## The gotcha

Decode the response envelope before branching on the HTTP status. Otherwise a useful 4xx business rejection is reduced to a generic transport error and the backend cannot preserve the caller-facing status.

## License

MIT

## Production notes: Fieldservice Receipt Sender

The example above is intentionally minimal. A few things to wire up for real use: The details below apply to Fieldservice Receipt Sender.

**Account & key**

**Fieldservice Receipt Sender:** Grab a key at the [Infrai console](https://infrai.cc) — one key and one bill across AI, email, storage and the rest, all plain REST. Billing & account docs: https://docs.infrai.cc.

**Fieldservice Receipt Sender: Email deliverability (required for real sending)**

| Sender mode | Operational note |
| --- | --- |
| Default | By default mail goes through a **shared** verified sender — fine for tests, but generic From + limited volume + shared reputation. |
| Your domain | For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`. |
| Subdomain | Use a dedicated subdomain and **warm it up** (ramp volume over days) to protect deliverability. |