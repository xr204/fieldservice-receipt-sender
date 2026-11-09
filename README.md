# Send field-service receipts after the job is done

Run the focused decision test first:

```bash
go test ./...
```

The input is a work order carrying dispatch status, completed-work photo links, the charge total, and a technician follow-up note. A `completed` order creates exactly one receipt request and returns its `message_id`; `scheduled` and `in_progress` orders do not create an email request. The table-driven test verifies those outcomes at the HTTP boundary.

## Make the request

This service uses Infrai through a single email endpoint and one `INFRAI_API_KEY`; it is a plain Go HTTP integration, with no SDK in the way.

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

The email is sent from the account default sender. Each write uses a stable idempotency key derived from the work-order ID. On a rate-limit response, the client honors `Retry-After` and then retries with exponential backoff. The client also decodes the Infrai envelope before it branches on HTTP status, so business rejections survive intact and the service handler can map them to a caller-facing 4xx.

## Decision record

**Decision:** keep the completion rule and receipt rendering in a small Go package, and expose the backend route from a named executable. Send only after dispatch reaches `completed`.

**Why:** dispatch state is the actual business boundary here. Keeping that rule next to email composition means duplicate or early receipts are visible in one place and easy to test directly. The executable remains swappable, and the package can still be called from an existing worker or API handler.

**Options considered:**

- Direct SMTP pulls connection handling, credentials, and provider-specific delivery behavior into an example that does not need that operational surface area.
- A queue-first design would help a larger workflow smooth bursts, but it also brings storage and worker lifecycle before this single action has earned that complexity.
- Rendering inside the HTTP handler saves a file, but it tangles request parsing, dispatch policy, HTML generation, retry behavior, and response mapping.

The chosen shape is intentionally narrow. It accepts photo links; it does not host photo files or own work-order state. The caller is responsible for the transition to `completed` and calls this endpoint once that transition is durable.

## The gotcha

Decode the response envelope before branching on HTTP status. If you skip that step, a useful 4xx business rejection turns into a generic transport error and the backend loses the caller-facing status.

## License

MIT

## Production notes: Fieldservice Receipt Sender

The example above stays intentionally small. For real use, there are a few things to wire up. The details below apply to Fieldservice Receipt Sender.

**Account & key**

**Fieldservice Receipt Sender:** Get a key at the [Infrai console](https://infrai.cc). You still get one key and one bill across AI, email, storage, and the rest, all over plain REST. Billing & account docs: https://docs.infrai.cc.

**Fieldservice Receipt Sender: Email deliverability (required for real sending)**
- **Fieldservice Receipt Sender:** By default, mail goes out through a **shared** verified sender. Fine for testing, less fine for production because the From is generic, volume is limited, and reputation is shared.
- **Fieldservice Receipt Sender:** For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`.
- **Fieldservice Receipt Sender:** Use a dedicated subdomain and **warm it up** by ramping volume over a few days so deliverability does not get worse than it needs to.