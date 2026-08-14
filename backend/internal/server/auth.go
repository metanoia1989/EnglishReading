package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type authUser struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Token string   `json:"token"`
	User  authUser `json:"user"`
}

// handleRegister is step 1: validate the account, persist a pending
// registration and return the simulated email verification code so the
// frontend can pop it up immediately (development mode).
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Nickname = strings.TrimSpace(req.Nickname)
	if !emailPattern.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, "邮箱格式不正确")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "密码至少需要 6 位")
		return
	}
	if req.Nickname == "" {
		req.Nickname = strings.SplitN(req.Email, "@", 2)[0]
	}

	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE email = ?`, req.Email).Scan(&exists)
	if err == nil {
		writeError(w, http.StatusConflict, "该邮箱已注册，请直接登录")
		return
	}
	if err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		log.Printf("register check: %v", err)
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	code := randomDigits(6)
	expires := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`
		INSERT INTO pending_registrations(email, password_hash, nickname, code, expires_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			password_hash = excluded.password_hash,
			nickname      = excluded.nickname,
			code          = excluded.code,
			expires_at    = excluded.expires_at`,
		req.Email, hash, req.Nickname, code, expires); err != nil {
		writeError(w, http.StatusInternalServerError, "验证码生成失败")
		log.Printf("insert pending registration: %v", err)
		return
	}

	// 模拟发送邮件：开发阶段把验证码直接交给前端弹出提示。
	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "验证码已发送（模拟邮件）",
		"devCode":   code,
		"expiresIn": 600,
	})
}

// handleVerifyRegister is step 2: check the code, create the user and issue
// a session token.
func (s *Server) handleVerifyRegister(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Code = strings.TrimSpace(req.Code)

	var pending struct {
		Hash     string
		Nickname string
		Expires  string
	}
	err := s.db.QueryRow(`
		SELECT password_hash, nickname, expires_at
		FROM pending_registrations WHERE email = ?`, req.Email).
		Scan(&pending.Hash, &pending.Nickname, &pending.Expires)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusBadRequest, "请先获取验证码")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, pending.Expires)
	if err != nil || time.Now().After(expiresAt) {
		writeError(w, http.StatusBadRequest, "验证码已过期，请重新获取")
		return
	}

	var storedCode string
	err = s.db.QueryRow(`SELECT code FROM pending_registrations WHERE email = ?`, req.Email).Scan(&storedCode)
	if err != nil || storedCode != req.Code {
		writeError(w, http.StatusBadRequest, "验证码不正确")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO users(email, nickname, password_hash) VALUES(?, ?, ?)`,
		req.Email, pending.Nickname, pending.Hash)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "该邮箱已注册，请直接登录")
		} else {
			writeError(w, http.StatusInternalServerError, "创建账号失败")
		}
		return
	}
	userID, err := res.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建账号失败")
		return
	}
	token, err := issueToken(tx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	if _, err := tx.Exec(`DELETE FROM pending_registrations WHERE email = ?`, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "清理验证记录失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "注册失败，请重试")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Token: token,
		User:  authUser{ID: userID, Email: req.Email, Nickname: pending.Nickname},
	})
}

func randomDigits(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n)
	}
	for i := range b {
		b[i] = byte('0' + int(b[i])%10)
	}
	return string(b)
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	var u authUser
	var hash string
	err := s.db.QueryRow(`SELECT id, email, nickname, password_hash FROM users WHERE email = ?`, req.Email).
		Scan(&u.ID, &u.Email, &u.Nickname, &hash)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusUnauthorized, "邮箱或密码不正确")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "邮箱或密码不正确")
		return
	}
	token, err := issueToken(s.db, u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败，请重试")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{Token: token, User: u})
}

// issueToken writes a random session token. It accepts either *sql.DB or
// *sql.Tx so it can be reused inside registration transactions.
type tokenInserter interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func issueToken(db tokenInserter, userID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO sessions(token, user_id, expires_at) VALUES(?, ?, ?)`,
		token, userID, expires)
	return token, err
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, _ authUser) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token != "" {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user authUser) {
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}
