// Package httpapi exposes the compatibility HTTP surface consumed by the
// existing Vue frontend. All business errors use HTTP 200 and a JSON code.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/auth"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/jumpserver"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// JobScheduler is the narrow scheduler surface required by the API.
type JobScheduler interface {
	NextRun(id string) (time.Time, bool)
	Remove(id string)
	UpdateInterval(id string, minutes int) error
	RunNow(ctx context.Context, id string) error
}

// Server owns routing and dependencies for the HTTP API.
type Server struct {
	store  store.Store
	cipher *cryptox.Cipher
	auth   *auth.Service
	jobs   JobScheduler
	jms    jumpserver.Syncer
	logger *slog.Logger
	mux    *http.ServeMux
}

// New builds the API server. jobs may be nil in read-only tests; handlers
// then degrade nextime to "无" and mutations return a business error.
func New(st store.Store, cipher *cryptox.Cipher, authSvc *auth.Service, jobs JobScheduler, logger *slog.Logger) *Server {
	return NewWithJMS(st, cipher, authSvc, jobs, nil, logger)
}

// NewWithJMS adds the real JMS sync dependency while retaining the read-only
// constructor used by compatibility tests and consumers.
func NewWithJMS(st store.Store, cipher *cryptox.Cipher, authSvc *auth.Service, jobs JobScheduler, jms jumpserver.Syncer, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{store: st, cipher: cipher, auth: authSvc, jobs: jobs, jms: jms, logger: logger}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/api/user/", s.handleUser)
	s.mux.HandleFunc("/api/jobs", s.withAuth(s.handleJobs))
	s.mux.HandleFunc("/api/edit/", s.withAuth(s.handleEdit))
	s.mux.HandleFunc("/api/nodes/", s.withAuth(s.handleNodes))
	s.mux.HandleFunc("/api/rds/", s.withAuth(s.handleRDS))
	s.mux.HandleFunc("/api/redis/", s.withAuth(s.handleRedis))
	s.mux.HandleFunc("/api/jms/", s.withAuth(s.handleJMS))
	s.mux.HandleFunc("/api/", s.notFound)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.auth.ParseAuthorization(r.Header.Get("Authorization")); err != nil {
			s.logger.Warn("authentication failed", "path", r.URL.Path, "error", err)
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 50000,
				"data": "403：认证异常，请重新登录！",
			})
			return
		}
		next(w, r)
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"code": 50000,
		"data": "404：接口不存在！",
	})
}

func (s *Server) methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
		"code": 50000,
		"data": "405：请求方法不支持！",
	})
}

func (s *Server) businessError(w http.ResponseWriter, err error) {
	if err != nil {
		s.logger.Error("request failed", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": 50000,
		"data": "请求处理失败，请查看后端日志！",
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}

func decodeJSONBody(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}

func queryDict(r *http.Request) (map[string]string, error) {
	raw := r.URL.Query().Get("query_dict")
	if raw == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, errors.New("query_dict is not valid JSON")
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func formatNext(t time.Time) string {
	return t.Format("01.02/15:04")
}

func requiredQuery(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}
