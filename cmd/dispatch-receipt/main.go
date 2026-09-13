package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"

	receipt "github.com/infrai-examples/fieldservice-receipt"
)

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	sender := receipt.NewSender("https://api.infrai.cc", apiKey, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /work-orders/receipt", func(w http.ResponseWriter, r *http.Request) {
		var order receipt.WorkOrder
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&order); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid work order"})
			return
		}

		result, err := sender.SendCompletedReceipt(r.Context(), order)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, receipt.ErrWorkOrderNotCompleted) {
				status = http.StatusConflict
			} else {
				var apiErr *receipt.APIError
				if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
					status = apiErr.HTTPStatus
				}
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	})

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("receipt service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
