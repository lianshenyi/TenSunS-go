package httpapi

import (
	"net/http"
	"strings"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	opt := strings.TrimPrefix(r.URL.Path, "/api/user/")
	switch opt {
	case "login":
		if r.Method != http.MethodPost {
			s.methodNotAllowed(w)
			return
		}
		var req loginRequest
		if err := decodeJSONBody(r, &req); err != nil {
			s.businessError(w, err)
			return
		}
		if !s.auth.CheckPassword(req.Password) {
			writeJSON(w, http.StatusOK, map[string]any{"code": 40000, "data": "密码错误！"})
			return
		}
		token, err := s.auth.NewToken()
		if err != nil {
			s.businessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 20000,
			"data": map[string]any{
				"token":    "Bearer " + token,
				"username": req.Username,
			},
		})
	case "logout":
		if r.Method != http.MethodPost {
			s.methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 20000, "data": "success"})
	case "info":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w)
			return
		}
		if _, err := s.auth.ParseAuthorization(r.Header.Get("Authorization")); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 50000,
				"data": "403：认证异常，请重新登录！",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 20000,
			"data": map[string]any{
				"roles":  []string{"admin"},
				"name":   "admin",
				"avatar": "/sl.png",
			},
		})
	default:
		s.notFound(w, r)
	}
}
