// Package api exposes the expense REST API used by the family dashboard.
//
// The dashboard has no login (an explicit product requirement), so the API
// is scoped to the configured family and is meant to be published on loopback
// only or behind a trusted access-control proxy. Mutating requests must be
// JSON and, when an Origin header is present, come from an allowed origin; this
// blocks drive-by cross-site form posts from other web pages.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

const maxBodyBytes = 16 << 10

// Server holds HTTP handlers.
type Server struct {
	svc      *expense.Service
	currency string
	origins  map[string]bool
	log      *slog.Logger
}

// New builds the API server.
func New(svc *expense.Service, currency string, allowedOrigins []string, log *slog.Logger) *Server {
	o := map[string]bool{}
	for _, v := range allowedOrigins {
		o[v] = true
	}
	return &Server{svc: svc, currency: currency, origins: o, log: log}
}

// Handler returns the root handler with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/expenses", s.list)
	mux.HandleFunc("POST /api/expenses", s.create)
	mux.HandleFunc("GET /api/expenses/{id}", s.get)
	mux.HandleFunc("PUT /api/expenses/{id}", s.update)
	mux.HandleFunc("DELETE /api/expenses/{id}", s.delete)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
	})
	return s.recoverer(s.logRequests(s.cors(mux)))
}

// ---- middleware ----

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin != "" && s.origins[origin]
		if allowed {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			if !allowed {
				writeError(w, http.StatusForbidden, "origin_not_allowed", "origin is not allowed", nil)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if isMutating(r.Method) && origin != "" && !allowed {
			writeError(w, http.StatusForbidden, "origin_not_allowed", "origin is not allowed", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutating(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete || m == http.MethodPatch
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Path only: no query strings or bodies, which may contain expense details.
		s.log.Info("http request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic in handler", "path", r.URL.Path, "panic", fmt.Sprint(v))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---- responses ----

type errorBody struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, fields map[string]string) {
	var b errorBody
	b.Error.Code, b.Error.Message, b.Error.Fields = code, msg, fields
	writeJSON(w, status, b)
}

func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *expense.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, "validation_error", "request validation failed", ve.Fields)
	case errors.Is(err, expense.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "expense not found", nil)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the request timed out; no changes were confirmed", nil)
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
		writeError(w, http.StatusServiceUnavailable, "unavailable", "storage is unavailable; no changes were confirmed", nil)
	}
}

// ---- DTOs ----

type expenseDTO struct {
	expense.Expense
	MemberName string `json:"member_name"`
}

func (s *Server) dto(e expense.Expense) expenseDTO {
	d := expenseDTO{Expense: e}
	for _, m := range s.svc.Members() {
		if m.ID == e.UserID {
			d.MemberName = m.Name
		}
	}
	return d
}

type expenseInput struct {
	UserID   *int64        `json:"user_id"`
	Amount   *money.Amount `json:"amount"`
	Category *string       `json:"category"`
	SpentAt  *string       `json:"spent_at"`
	Comment  *string       `json:"comment"`
}

func (s *Server) decodeInput(w http.ResponseWriter, r *http.Request) (expense.Fields, bool) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", nil)
		return expense.Fields{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in expenseInput
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large", nil)
			return expense.Fields{}, false
		}
		if errors.Is(err, money.ErrSyntax) || errors.Is(err, money.ErrPrecision) || errors.Is(err, money.ErrTooLarge) || errors.Is(err, money.ErrEmpty) {
			writeError(w, http.StatusBadRequest, "validation_error", "request validation failed", map[string]string{"amount": err.Error()})
			return expense.Fields{}, false
		}
		msg := "request body must be a valid JSON object"
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			msg = err.Error()
		}
		writeError(w, http.StatusBadRequest, "invalid_body", msg, nil)
		return expense.Fields{}, false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must contain a single JSON object", nil)
		return expense.Fields{}, false
	}

	ve := map[string]string{}
	var f expense.Fields
	if in.UserID == nil {
		ve["user_id"] = "user_id is required"
	} else {
		f.UserID = *in.UserID
	}
	if in.Amount == nil {
		ve["amount"] = money.ErrEmpty.Error()
	} else {
		f.Amount = *in.Amount
	}
	if in.Category == nil {
		ve["category"] = "category is required"
	} else {
		f.Category = expense.Category(*in.Category)
	}
	if in.SpentAt == nil {
		ve["spent_at"] = "spent_at is required"
	} else if d, err := dates.Parse(*in.SpentAt); err != nil {
		ve["spent_at"] = err.Error()
	} else {
		f.SpentAt = d
	}
	f.Comment = in.Comment
	if len(ve) > 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "request validation failed", ve)
		return expense.Fields{}, false
	}
	return f, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "request validation failed", map[string]string{"id": "id must be a positive integer"})
		return 0, false
	}
	return id, true
}

// ---- handlers ----

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.svc.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"currency":   s.currency,
		"timezone":   s.svc.Location().String(),
		"today":      s.svc.Today(),
		"categories": expense.Categories,
		"members":    s.svc.Members(),
		"max_limit":  expense.MaxLimit,
	})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ve := map[string]string{}
	var f expense.Filter
	for key := range q {
		switch key {
		case "user_id", "from", "to", "category", "limit", "offset":
		default:
			ve[key] = "unknown query parameter"
		}
	}
	if v := q.Get("user_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			ve["user_id"] = "user_id must be a positive integer"
		} else {
			f.UserID = &id
		}
	}
	if v := q.Get("category"); v != "" {
		c, err := expense.ParseCategory(v)
		if err != nil {
			ve["category"] = err.Error()
		} else {
			f.Category = &c
		}
	}
	// Half-open range: from is inclusive, to is exclusive. Either bound may be
	// omitted.
	var rng dates.Range
	if v := q.Get("from"); v != "" {
		d, err := dates.Parse(v)
		if err != nil {
			ve["from"] = err.Error()
		}
		rng.From = d
	}
	if v := q.Get("to"); v != "" {
		d, err := dates.Parse(v)
		if err != nil {
			ve["to"] = err.Error()
		}
		rng.To = d
	}
	if !rng.From.IsZero() || !rng.To.IsZero() {
		if rng.From.IsZero() {
			rng.From = expense.MinSpentAt
		}
		if rng.To.IsZero() {
			rng.To = dates.New(9999, 1, 1)
		}
		f.Period = &rng
	}
	for _, p := range []struct {
		key string
		dst *int
	}{{"limit", &f.Limit}, {"offset", &f.Offset}} {
		if v := q.Get(p.key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				ve[p.key] = p.key + " must be an integer"
			}
			*p.dst = n
		}
	}
	if q.Get("limit") == "0" {
		ve["limit"] = "limit must be between 1 and 200"
	}
	if len(ve) > 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "request validation failed", ve)
		return
	}
	page, err := s.svc.List(r.Context(), s.svc.FamilyScope(), f)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	items := make([]expenseDTO, len(page.Items))
	for i, e := range page.Items {
		items[i] = s.dto(e)
	}
	limit := f.Limit
	if limit == 0 {
		limit = expense.DefaultLimit
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    page.Total,
		"sum":      page.Sum,
		"currency": s.currency,
		"limit":    limit,
		"offset":   f.Offset,
	})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := s.svc.Get(r.Context(), s.svc.FamilyScope(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.dto(e))
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	f, ok := s.decodeInput(w, r)
	if !ok {
		return
	}
	e, err := s.svc.Create(r.Context(), s.svc.FamilyScope(), f)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/expenses/%d", e.ID))
	writeJSON(w, http.StatusCreated, s.dto(e))
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, ok := s.decodeInput(w, r)
	if !ok {
		return
	}
	e, err := s.svc.Update(r.Context(), s.svc.FamilyScope(), id, f)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.dto(e))
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.svc.Delete(r.Context(), s.svc.FamilyScope(), id); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
