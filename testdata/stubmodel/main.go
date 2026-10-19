// Command stubmodel is an OpenAI-compatible endpoint that answers from a table instead of a model.
//
// It exists so that the commands which score a real model can be run in CI, where there is no endpoint
// and no key: `make baseline-model` and `make eval-model` are pointed at this, and the guard in
// scripts/check-model-path.sh asserts they produce a report and a comparison. Without it, the model
// column of this repository is only ever exercised by a person who happens to have an endpoint - which
// is exactly how `make eval-model` came to be broken in three different ways at once.
//
//	go run ./testdata/stubmodel -addr 127.0.0.1:18999
//
// It answers every request with the same envelope. That makes its metrics worthless on purpose: what
// is being tested is the path, not the quality, and a fixture that looked like a decent classifier
// would invite somebody to quote it.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
)

const answer = `{"label": "retryable", "confidence": 0.95}`

func main() {
	addr := flag.String("addr", "127.0.0.1:18999", "address to listen on")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		response := map[string]any{
			"id":     "stub",
			"object": "chat.completion",
			"model":  body.Model,
			"choices": []any{map[string]any{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": answer},
			}},
			"usage": map[string]any{"prompt_tokens": 42, "completion_tokens": 11, "total_tokens": 53},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})

	log.Printf("stub model on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
