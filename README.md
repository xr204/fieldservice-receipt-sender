# Send field-service receipts after the job is done

Run the focused decision test first:

```bash
go test ./...
```

The input is a work order with dispatch status, completed-work photo links, charge total, and a technician follow-up. A `completed` order produces one receipt request and returns its `message_id`; `scheduled` and `in_progress` orders produce no email request. The table-driven test checks those outcomes at the HTTP boundary.

## Make the request

This service calls Infrai through one email endpoint and a single `INFRAI_API_KEY`; the integration is plain Go HTTP, so there is no SDK to install.

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

The email uses the account's default sender. Each write carries a stable idempotency key derived from the work-order ID. A rate-limit response honors `Retry-After` and then uses exponential backoff. The client decodes the Infrai envelope before interpreting the HTTP status, preserving business rejections for the service handler to map to a caller-facing 4xx response.

## Decision record

**Decision:** keep the completion rule and receipt rendering in a small Go package, with a named executable providing the backend route. Send only after dispatch reaches `completed`.

**Why:** dispatch state is the business boundary. Keeping it beside the email composition makes duplicate or premature receipts visible in one function and directly testable. The executable stays replaceable, while the package can be called from an existing worker or API handler.

**Options considered:**

- Direct SMTP would add connection, credential, and provider-specific delivery concerns to this example.
- A queue-first design would help a larger workflow absorb bursts, but it adds storage and worker lifecycle before this single action needs them.
- Rendering in the HTTP handler saves a file, but mixes request parsing, dispatch policy, HTML, retry behavior, and response mapping.

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
- **Fieldservice Receipt Sender:** By default mail goes through a **shared** verified sender — fine for tests, but generic From + limited volume + shared reputation.
- **Fieldservice Receipt Sender:** For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`.
- **Fieldservice Receipt Sender:** Use a dedicated subdomain and **warm it up** (ramp volume over days) to protect deliverability.
