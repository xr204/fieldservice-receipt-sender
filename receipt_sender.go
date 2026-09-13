package receipt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const sendEmailPath = "/v1/email/send"

var ErrWorkOrderNotCompleted = errors.New("receipt requires a completed work order")

type WorkOrder struct {
	ID                 string   `json:"id"`
	CustomerEmail      string   `json:"customer_email"`
	CustomerName       string   `json:"customer_name"`
	DispatchStatus     string   `json:"dispatch_status"`
	ServiceSummary     string   `json:"service_summary"`
	TotalCents         int64    `json:"total_cents"`
	PhotoURLs          []string `json:"photo_urls"`
	TechnicianName     string   `json:"technician_name"`
	TechnicianFollowUp string   `json:"technician_follow_up"`
}

type Receipt struct {
	MessageID string `json:"message_id"`
}

type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type Sender struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
}

func NewSender(baseURL, apiKey string, client *http.Client) *Sender {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: client,
		sleep:      sleepContext,
	}
}

// SendCompletedReceipt implements email.send with POST /v1/email/send.
func (s *Sender) SendCompletedReceipt(ctx context.Context, order WorkOrder) (Receipt, error) {
	if order.DispatchStatus != "completed" {
		return Receipt{}, ErrWorkOrderNotCompleted
	}
	if order.ID == "" || order.CustomerEmail == "" {
		return Receipt{}, errors.New("work order id and customer email are required")
	}

	htmlBody, err := renderReceipt(order)
	if err != nil {
		return Receipt{}, err
	}
	payload := struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		HTML    string `json:"html"`
	}{
		To:      order.CustomerEmail,
		Subject: "Service receipt for work order " + order.ID,
		HTML:    htmlBody,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, fmt.Errorf("encode email: %w", err)
	}

	for attempt := 0; attempt < 4; attempt++ {
		receipt, retryAfter, err := s.send(ctx, order.ID, body)
		if err == nil {
			return receipt, nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusTooManyRequests || attempt == 3 {
			return Receipt{}, err
		}
		delay := time.Duration(1<<attempt) * time.Second
		if retryAfter > delay {
			delay = retryAfter
		}
		if err := s.sleep(ctx, delay); err != nil {
			return Receipt{}, err
		}
	}
	return Receipt{}, errors.New("email send attempts exhausted")
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

func (s *Sender) send(ctx context.Context, orderID string, body []byte) (Receipt, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+sendEmailPath, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, 0, fmt.Errorf("build email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "work-order-receipt-"+orderID)

	res, err := s.httpClient.Do(req)
	if err != nil {
		return Receipt{}, 0, fmt.Errorf("send email request: %w", err)
	}
	defer res.Body.Close()

	limited := io.LimitReader(res.Body, 1<<20)
	var env envelope
	if err := json.NewDecoder(limited).Decode(&env); err != nil {
		return Receipt{}, 0, fmt.Errorf("decode email response (status %d): %w", res.StatusCode, err)
	}
	if !env.OK {
		apiErr := &APIError{HTTPStatus: res.StatusCode, Message: "email request rejected"}
		if env.Error != nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
			if apiErr.Message == "" {
				apiErr.Message = env.Error.Hint
			}
		}
		return Receipt{}, parseRetryAfter(res.Header.Get("Retry-After")), apiErr
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Receipt{}, 0, fmt.Errorf("email transport status %d", res.StatusCode)
	}
	var receipt Receipt
	if err := json.Unmarshal(env.Data, &receipt); err != nil {
		return Receipt{}, 0, fmt.Errorf("decode email result: %w", err)
	}
	if receipt.MessageID == "" {
		return Receipt{}, 0, errors.New("email result omitted message_id")
	}
	return receipt, 0, nil
}

func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var receiptTemplate = template.Must(template.New("receipt").Funcs(template.FuncMap{
	"money": func(cents int64) string {
		return fmt.Sprintf("$%.2f", float64(cents)/100)
	},
}).Parse(`<!doctype html>
<html><body>
<h1>Work order {{.ID}} complete</h1>
<p>Hello {{.CustomerName}},</p>
<p>{{.ServiceSummary}}</p>
<p><strong>Total: {{money .TotalCents}}</strong></p>
{{if .PhotoURLs}}<h2>Work photos</h2><ul>{{range .PhotoURLs}}<li><a href="{{.}}">View photo</a></li>{{end}}</ul>{{end}}
<h2>Technician follow-up</h2>
<p>{{.TechnicianName}}: {{.TechnicianFollowUp}}</p>
</body></html>`))

func renderReceipt(order WorkOrder) (string, error) {
	var output bytes.Buffer
	if err := receiptTemplate.Execute(&output, order); err != nil {
		return "", fmt.Errorf("render receipt: %w", err)
	}
	return output.String(), nil
}
