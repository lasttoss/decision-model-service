// Package api is the HTTP surface: one decision endpoint, one health endpoint, one
// metrics endpoint. There is no authentication here on purpose and it is stated in the
// README rather than implied: this is a decision helper that a log pipeline calls inside
// a cluster, not a public service, and inventing a token scheme for a demo would teach
// nothing about the part of this repository that is worth reading.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lasttoss/decision-model-service/internal/decide"
	"github.com/lasttoss/decision-model-service/internal/metrics"
)

type Server struct {
	Service *decide.Service
	// MaxInputBytes bounds the input. A log pipeline that sends a 10MB stack trace is a
	// misconfiguration, and answering it would cost a model call.
	MaxInputBytes int64
	Started       time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("POST /v1/decide", s.decide)
	return mux
}

type decideRequest struct {
	Input string `json:"input"`
	// Explain asks for a model-written sentence in addition to the label. It is off by
	// default because it is the only part of this service that costs output tokens.
	Explain *bool `json:"explain,omitempty"`
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"providers": s.Service.Names(),
		"threshold": s.Service.Threshold,
		"explain":   s.Service.Explain,
	})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	counters := s.Service.Counters
	if counters == nil {
		counters = metrics.New()
	}
	_, _ = w.Write([]byte(metrics.Render(counters.Snapshot())))
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	max := s.MaxInputBytes
	if max <= 0 {
		max = 64 << 10
	}
	var req decideRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Input) == "" {
		// An empty input is a caller mistake. Answering needs_human for it would put a
		// meaningless line into the training data of whoever reads the trail.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "input is required"})
		return
	}

	service := s.Service
	if req.Explain != nil && *req.Explain != service.Explain {
		// The request-level flag is a copy of the service with the flag flipped, rather
		// than a field on the shared service: two concurrent requests asking for different
		// things must not be able to change each other's behaviour.
		copied := *service
		copied.Explain = *req.Explain
		service = &copied
	}

	out := service.Decide(r.Context(), req.Input)
	// Always 200. A degraded answer is an answer, and the caller can see that it is one;
	// returning 503 would make a pipeline drop the line, which is strictly worse than
	// being told "needs_human, low confidence".
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		log.Printf("write response: %v", err)
	}
}
