package receipt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendCompletedReceiptDecision(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		wantCalls int
		wantErr   error
	}{
		{name: "completed dispatch sends", status: "completed", wantCalls: 1},
		{name: "scheduled dispatch does not send", status: "scheduled", wantErr: ErrWorkOrderNotCompleted},
		{name: "in progress dispatch does not send", status: "in_progress", wantErr: ErrWorkOrderNotCompleted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != sendEmailPath {
					t.Fatalf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("missing bearer authorization")
				}
				if r.Header.Get("Idempotency-Key") != "work-order-receipt-WO-1042" {
					t.Fatal("missing stable idempotency key")
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if len(payload) != 3 || payload["to"] != "ada@example.com" {
					t.Fatalf("payload = %#v", payload)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"data":{"message_id":"msg_1042"},"error":null,"metadata":{}}`))
			}))
			defer server.Close()

			sender := NewSender(server.URL, "test-key", server.Client())
			got, err := sender.SendCompletedReceipt(context.Background(), sampleOrder(tt.status))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tt.wantCalls)
			}
			if tt.wantErr == nil && got.MessageID != "msg_1042" {
				t.Fatalf("message id = %q", got.MessageID)
			}
		})
	}
}

func TestSendCompletedReceiptRetriesRateLimit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"data":null,"error":{"code":"RATE_LIMITED","message":"retry later"},"metadata":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"message_id":"msg_retry"},"error":null,"metadata":{}}`))
	}))
	defer server.Close()

	sender := NewSender(server.URL, "test-key", server.Client())
	var delay time.Duration
	sender.sleep = func(_ context.Context, got time.Duration) error { delay = got; return nil }
	receipt, err := sender.SendCompletedReceipt(context.Background(), sampleOrder("completed"))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || delay != time.Second || receipt.MessageID != "msg_retry" {
		t.Fatalf("calls=%d delay=%s receipt=%+v", calls, delay, receipt)
	}
}

func sampleOrder(status string) WorkOrder {
	return WorkOrder{
		ID:                 "WO-1042",
		CustomerEmail:      "ada@example.com",
		CustomerName:       "Ada",
		DispatchStatus:     status,
		ServiceSummary:     "Replaced the failed condenser contactor.",
		TotalCents:         18950,
		PhotoURLs:          []string{"https://photos.example.com/work-orders/WO-1042/after.jpg"},
		TechnicianName:     "Mina",
		TechnicianFollowUp: "Check the filter again in 30 days.",
	}
}
