package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"english-reading/backend/internal/store"
)

type authUser struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// errEmailTaken lets the registration transaction report a duplicate account
// without inspecting driver-specific error text.
var errEmailTaken = errors.New("email already registered")

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

	var existing int64
	if err := s.db.Model(&store.User{}).Where("email = ?", req.Email).
		Count(&existing).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		log.Printf("register check: %v", err)
		return
	}
	if existing > 0 {
		writeError(w, http.StatusConflict, "该邮箱已注册，请直接登录")
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	code := randomDigits(6)
	pending := store.PendingRegistration{
		Email:        req.Email,
		PasswordHash: hash,
		Nickname:     req.Nickname,
		Code:         code,
		ExpiresAt:    time.Now().Add(10 * time.Minute).UTC(),
	}
	// Upsert: re-requesting a code replaces the previous pending registration.
	if err := s.db.Clauses(upsertOn("email", "password_hash", "nickname", "code", "expires_at")).
		Create(&pending).Error; err != nil {
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

	var pending store.PendingRegistration
	err := s.db.Where("email = ?", req.Email).Take(&pending).Error
	if store.IsNotFound(err) {
		writeError(w, http.StatusBadRequest, "请先获取验证码")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		return
	}
	if time.Now().After(pending.ExpiresAt) {
		writeError(w, http.StatusBadRequest, "验证码已过期，请重新获取")
		return
	}
	if pending.Code != req.Code {
		writeError(w, http.StatusBadRequest, "验证码不正确")
		return
	}

	var (
		token string
		user  authUser
	)
	err = s.db.Transaction(func(tx *gorm.DB) error {
		record := store.User{
			Email:        req.Email,
			Nickname:     pending.Nickname,
			PasswordHash: pending.PasswordHash,
		}
		if err := tx.Create(&record).Error; err != nil {
			if store.IsDuplicate(err) {
				return errEmailTaken
			}
			return err
		}
		t, err := issueToken(tx, record.ID)
		if err != nil {
			return err
		}
		token = t
		user = authUser{ID: record.ID, Email: record.Email, Nickname: record.Nickname}
		return tx.Where("email = ?", req.Email).Delete(&store.PendingRegistration{}).Error
	})
	switch {
	case errors.Is(err, errEmailTaken):
		writeError(w, http.StatusConflict, "该邮箱已注册，请直接登录")
	case err != nil:
		log.Printf("verify register: %v", err)
		writeError(w, http.StatusInternalServerError, "注册失败，请重试")
	default:
		writeJSON(w, http.StatusOK, sessionResponse{Token: token, User: user})
	}
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

	var record store.User
	err := s.db.Where("email = ?", req.Email).Take(&record).Error
	if store.IsNotFound(err) {
		writeError(w, http.StatusUnauthorized, "邮箱或密码不正确")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器开小差了")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "邮箱或密码不正确")
		return
	}
	token, err := issueToken(s.db, record.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败，请重试")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Token: token,
		User:  authUser{ID: record.ID, Email: record.Email, Nickname: record.Nickname},
	})
}

// issueToken writes a random session token. It accepts either *gorm.DB or
// *gorm.DB inside a transaction, since GORM's *gorm.DB is the same type in
// both cases.
func issueToken(db *gorm.DB, userID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	session := store.Session{
		Token:     token,
		UserID:    userID,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UTC(),
	}
	if err := db.Create(&session).Error; err != nil {
		return "", err
	}
	return token, nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, _ authUser) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token != "" {
		if err := s.db.Where("token = ?", token).Delete(&store.Session{}).Error; err != nil {
			log.Printf("logout: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user authUser) {
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}
