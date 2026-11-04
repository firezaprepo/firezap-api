// Package httpapi exposes the REST API.
//
// Authentication: "Authorization: Bearer <key>". Admin routes need the admin key (FZ_ADMIN_KEY);
// instance routes accept the admin key or the key of that instance.
package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/firezaprepo/firezap-api/internal/auth"
	"github.com/firezaprepo/firezap-api/internal/instance"
	"github.com/firezaprepo/firezap-api/internal/store"
)

// Version is the API version reported by the root endpoint.
const Version = "0.1.0-dev"

const (
	maxBody       = 1 << 20 // 1 MB
	maxTextLength = 4096
	maxNameLength = 80
)

// Server holds the HTTP handlers.
type Server struct {
	adminKey string
	manager  *instance.Manager
	store    *store.Store
	log      *slog.Logger
}

// New creates the server.
func New(adminKey string, manager *instance.Manager, st *store.Store, log *slog.Logger) *Server {
	return &Server{adminKey: adminKey, manager: manager, store: st, log: log}
}

// Handler returns the router with every route.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.root)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /instances", s.admin(s.createInstance))
	mux.HandleFunc("GET /instances", s.admin(s.listInstances))
	mux.HandleFunc("DELETE /instances/{id}", s.admin(s.deleteInstance))

	mux.HandleFunc("GET /instances/{id}", s.inst(s.getInstance))
	mux.HandleFunc("POST /instances/{id}/connect", s.inst(s.connect))
	mux.HandleFunc("GET /instances/{id}/qr", s.inst(s.qr))
	mux.HandleFunc("POST /instances/{id}/pair", s.inst(s.pair))
	mux.HandleFunc("POST /instances/{id}/logout", s.inst(s.logout))
	mux.HandleFunc("PUT /instances/{id}/webhook", s.inst(s.setWebhook))
	mux.HandleFunc("POST /instances/{id}/messages/text", s.inst(s.sendText))

	return s.logRequests(mux)
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    "FireZAP API",
		"version": Version,
		"docs":    "https://github.com/firezaprepo/firezap-api",
	})
}

// ── Authentication ──────────────────────────────────────────────────────────

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.Equal(auth.BearerToken(r.Header.Get("Authorization")), s.adminKey) {
			writeError(w, http.StatusUnauthorized, "invalid or missing admin key")
			return
		}
		next(w, r)
	}
}

func (s *Server) inst(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := auth.BearerToken(r.Header.Get("Authorization"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing API key")
			return
		}
		if auth.Equal(token, s.adminKey) {
			next(w, r)
			return
		}
		rec, err := s.store.GetByKeyHash(r.Context(), auth.HashKey(token))
		// The same answer for "unknown key" and "key of another instance": never reveal which
		// instances exist to someone holding a different key.
		if err != nil || rec.ID != r.PathValue("id") {
			writeError(w, http.StatusUnauthorized, "invalid API key for this instance")
			return
		}
		next(w, r)
	}
}

// ── Handlers ────────────────────────────────────────────────────────────────

func (s *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.ID = strings.TrimSpace(strings.ToLower(body.ID))
	body.Name = strings.TrimSpace(body.Name)
	if !auth.ValidInstanceID(body.ID) {
		writeError(w, http.StatusBadRequest, "id must have 3 to 40 lowercase letters, digits or hyphens")
		return
	}
	if body.Name == "" {
		body.Name = body.ID
	}
	if utf8.RuneCountInString(body.Name) > maxNameLength {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	info, key, err := s.manager.Create(r.Context(), body.ID, body.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"instance": info,
		"apiKey":   key,
		"note":     "Save the apiKey now: it is shown only once.",
	})
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"instances": s.manager.List()})
}

func (s *Server) getInstance(w http.ResponseWriter, r *http.Request) {
	info, err := s.manager.Get(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.manager.Connect(id); err != nil {
		s.fail(w, err)
		return
	}
	info, _ := s.manager.Get(id)
	writeJSON(w, http.StatusAccepted, info)
}

func (s *Server) qr(w http.ResponseWriter, r *http.Request) {
	q, err := s.manager.QR(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	resp := map[string]any{"status": q.Status}
	if q.Code != "" {
		resp["code"] = q.Code
		resp["expiresAt"] = q.ExpiresAt
		if png, err := qrcode.Encode(q.Code, qrcode.Medium, 512); err == nil {
			resp["image"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	code, err := s.manager.Pair(ctx, r.PathValue("id"), body.Phone)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"code": code,
		"hint": "On the phone: WhatsApp > Linked devices > Link a device > Link with phone number instead.",
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Logout(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(instance.StatusLoggedOut)})
}

func (s *Server) setWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if body.URL != "" {
		u, err := url.Parse(body.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			writeError(w, http.StatusBadRequest, "url must be an http or https address")
			return
		}
	}
	secret, err := s.manager.SetWebhook(r.Context(), r.PathValue("id"), body.URL, strings.TrimSpace(body.Secret))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": body.URL, "secret": secret})
}

func (s *Server) sendText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if utf8.RuneCountInString(body.Text) > maxTextLength {
		writeError(w, http.StatusBadRequest, "text is too long (max 4096 characters)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	id, err := s.manager.SendText(ctx, r.PathValue("id"), body.To, body.Text)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "instance not found")
	case errors.Is(err, instance.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, instance.ErrNotConnected), errors.Is(err, instance.ErrAlreadyLinked):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, instance.ErrInvalidRecipient):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "the operation timed out")
	default:
		s.log.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
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
		// The Authorization header is never logged.
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}
