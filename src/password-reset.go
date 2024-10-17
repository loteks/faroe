package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"faroe/argon2id"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"
)

func handleCreatePasswordResetRequestRequest(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	clientIP := r.Header.Get("X-Client-IP")

	if !verifySecret(r) {
		writeNotAuthenticatedErrorResponse(w)
		return
	}
	if !verifyJSONContentTypeHeader(r) {
		writeUnsupportedMediaTypeErrorResponse(w)
		return
	}
	if !verifyJSONAcceptHeader(r) {
		writeNotAcceptableErrorResponse(w)
		return
	}

	body, err := io.ReadAll(r.Body)

	if err != nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	var data struct {
		Email *string `json:"email"`
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	if data.Email == nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	email := *data.Email

	if !verifyEmailInput(email) {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}

	user, err := getUserFromEmail(email)
	if errors.Is(err, ErrRecordNotFound) {
		writeExpectedErrorResponse(w, ExpectedErrorUserNotExists)
		return
	}
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	if clientIP != "" && !passwordHashingIPRateLimit.Consume(clientIP, 1) {
		writeExpectedErrorResponse(w, ExpectedErrorTooManyRequests)
		return
	}
	if clientIP != "" && !createPasswordResetIPRateLimit.Consume(clientIP, 1) {
		writeExpectedErrorResponse(w, ExpectedErrorTooManyRequests)
		return
	}

	code, err := generateSecureCode()
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}

	codeHash, err := argon2id.Hash(code)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}

	resetRequest, err := createPasswordResetRequest(user.Id, codeHash)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(resetRequest.EncodeToJSONWithCode(code)))
}

func handleGetPasswordResetRequestRequest(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	if !verifySecret(r) {
		writeNotAuthenticatedErrorResponse(w)
		return
	}
	if !verifyJSONAcceptHeader(r) {
		writeNotAcceptableErrorResponse(w)
		return
	}

	resetRequestId := params.ByName("request_id")
	resetRequest, err := getPasswordResetRequest(resetRequestId)
	if errors.Is(err, ErrRecordNotFound) {
		writeNotFoundErrorResponse(w)
		return
	}
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	// If now is or after expiration
	if time.Now().Compare(resetRequest.ExpiresAt) >= 0 {
		err = deletePasswordResetRequest(resetRequest.Id)
		if err != nil {
			log.Println(err)
			writeUnExpectedErrorResponse(w)
			return
		}
		writeNotFoundErrorResponse(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(resetRequest.EncodeToJSON()))
}

func handleVerifyPasswordResetRequestEmailRequest(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	clientIP := r.Header.Get("X-Client-IP")
	if !verifySecret(r) {
		writeNotAuthenticatedErrorResponse(w)
		return
	}
	if !verifyJSONContentTypeHeader(r) {
		writeUnsupportedMediaTypeErrorResponse(w)
		return
	}

	resetRequestId := params.ByName("request_id")
	resetRequest, err := getPasswordResetRequest(resetRequestId)
	if errors.Is(err, ErrRecordNotFound) {
		writeNotFoundErrorResponse(w)
		return
	}
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	// If now is or after expiration
	if time.Now().Compare(resetRequest.ExpiresAt) >= 0 {
		err = deletePasswordResetRequest(resetRequest.Id)
		if err != nil {
			log.Println(err)
			writeUnExpectedErrorResponse(w)
			return
		}
		writeNotFoundErrorResponse(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	var data struct {
		Code *string `json:"code"`
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	if data.Code == nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	if clientIP != "" && !passwordHashingIPRateLimit.Consume(clientIP, 1) {
		writeExpectedErrorResponse(w, ExpectedErrorTooManyRequests)
		return
	}
	if !verifyPasswordResetCodeLimitCounter.Consume(resetRequest.Id) {
		err = deletePasswordResetRequest(resetRequest.Id)
		if err != nil {
			log.Println(err)
			writeUnExpectedErrorResponse(w)
			return
		}
		writeExpectedErrorResponse(w, ExpectedErrorTooManyRequests)
		return
	}
	validCode, err := argon2id.Verify(resetRequest.CodeHash, *data.Code)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	if !validCode {
		writeExpectedErrorResponse(w, ExpectedErrorIncorrectCode)
		return
	}

	w.WriteHeader(204)
}

func handleResetPasswordRequest(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	if !verifySecret(r) {
		writeNotAuthenticatedErrorResponse(w)
		return
	}
	if !verifyJSONContentTypeHeader(r) {
		writeUnsupportedMediaTypeErrorResponse(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	var data struct {
		RequestId *string `json:"request_id"`
		Password  *string `json:"password"`
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}

	if data.RequestId == nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	if data.Password == nil {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	resetRequestId, password := *data.RequestId, *data.Password
	if len(password) > 127 {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidData)
		return
	}
	strongPassword, err := verifyPasswordStrength(password)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	if !strongPassword {
		writeExpectedErrorResponse(w, ExpectedErrorWeakPassword)
		return
	}
	passwordHash, err := argon2id.Hash(password)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}

	resetRequest, err := getPasswordResetRequest(resetRequestId)
	log.Println(err)
	if errors.Is(err, ErrRecordNotFound) {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidRequest)
		return
	}
	if err != nil {
		writeUnExpectedErrorResponse(w)
		return
	}
	// If now is or after expiration
	if time.Now().Compare(resetRequest.ExpiresAt) >= 0 {
		err = deletePasswordResetRequest(resetRequestId)
		if err != nil {
			log.Println(err)
			writeUnExpectedErrorResponse(w)
			return
		}
		writeExpectedErrorResponse(w, ExpectedErrorInvalidRequest)
		return
	}

	validResetRequest, err := resetUserPasswordWithPasswordResetRequest(resetRequest.Id, passwordHash)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	if !validResetRequest {
		writeExpectedErrorResponse(w, ExpectedErrorInvalidRequest)
		return
	}

	w.WriteHeader(204)
}

func handleDeletePasswordResetRequestRequest(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	if !verifySecret(r) {
		writeNotAuthenticatedErrorResponse(w)
		return
	}
	if !verifyJSONAcceptHeader(r) {
		writeNotAcceptableErrorResponse(w)
		return
	}

	resetRequestId := params.ByName("request_id")
	resetRequest, err := getPasswordResetRequest(resetRequestId)
	if errors.Is(err, ErrRecordNotFound) {
		writeNotFoundErrorResponse(w)
		return
	}
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}
	// If now is or after expiration
	if time.Now().Compare(resetRequest.ExpiresAt) >= 0 {
		err = deletePasswordResetRequest(resetRequest.Id)
		if err != nil {
			log.Println(err)
			writeUnExpectedErrorResponse(w)
			return
		}
		writeNotFoundErrorResponse(w)
		return
	}

	err = deletePasswordResetRequest(resetRequest.Id)
	if err != nil {
		log.Println(err)
		writeUnExpectedErrorResponse(w)
		return
	}

	w.WriteHeader(204)
}

func createPasswordResetRequest(userId string, codeHash string) (PasswordResetRequest, error) {
	now := time.Now()
	id, err := generateId()
	if err != nil {
		return PasswordResetRequest{}, nil
	}
	expiresAt := now.Add(10 * time.Minute)
	_, err = db.Exec("INSERT INTO password_reset_request (id, user_id, created_at, expires_at, code_hash) VALUES (?, ?, ?, ?, ?)", id, userId, now.Unix(), expiresAt.Unix(), codeHash)
	if err != nil {
		return PasswordResetRequest{}, err
	}
	request := PasswordResetRequest{
		Id:        id,
		UserId:    userId,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		CodeHash:  codeHash,
	}
	return request, nil
}

func getPasswordResetRequest(requestId string) (PasswordResetRequest, error) {
	var request PasswordResetRequest
	var createdAtUnix, expiresAtUnix int64
	row := db.QueryRow("SELECT id, user_id, created_at, code_hash, expires_at FROM password_reset_request WHERE id = ?", requestId)
	err := row.Scan(&request.Id, &request.UserId, &createdAtUnix, &request.CodeHash, &expiresAtUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordResetRequest{}, ErrRecordNotFound
	}
	if err != nil {
		return PasswordResetRequest{}, err
	}
	request.CreatedAt = time.Unix(createdAtUnix, 0)
	request.ExpiresAt = time.Unix(expiresAtUnix, 0)
	return request, nil
}

func resetUserPasswordWithPasswordResetRequest(requestId string, passwordHash string) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	var userId string
	err = db.QueryRow("DELETE FROM password_reset_request WHERE id = ? AND expires_at > ? RETURNING user_id", requestId, time.Now().Unix()).Scan(&userId)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.Commit()
		if err != nil {
			tx.Rollback()
			return false, err
		}
		return false, nil
	}
	if err != nil {
		tx.Rollback()
		return false, err
	}
	_, err = db.Exec("DELETE FROM password_reset_request WHERE user_id = ?", userId)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	_, err = db.Exec("UPDATE user SET password_hash = ? WHERE id = ?", passwordHash, userId)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	tx.Commit()
	return true, nil
}

func setPasswordResetRequestAsTwoFactorVerified(requestId string) error {
	_, err := db.Exec("UPDATE password_reset_request SET two_factor_verified = 1 WHERE id = ?", requestId)
	return err
}

func deletePasswordResetRequest(requestId string) error {
	_, err := db.Exec("DELETE FROM password_reset_request WHERE id = ?", requestId)
	return err
}

type PasswordResetRequest struct {
	Id        string
	UserId    string
	CreatedAt time.Time
	ExpiresAt time.Time
	CodeHash  string
}

func (r *PasswordResetRequest) EncodeToJSON() string {
	encoded := fmt.Sprintf("{\"id\":\"%s\",\"user_id\":\"%s\",\"created_at\":%d,\"expires_at\":%d}", r.Id, r.UserId, r.CreatedAt.Unix(), r.ExpiresAt.Unix())
	return encoded
}

func (r *PasswordResetRequest) EncodeToJSONWithCode(code string) string {
	encoded := fmt.Sprintf("{\"id\":\"%s\",\"user_id\":\"%s\",\"created_at\":%d,\"expires_at\":%d,\"code\":\"%s\"}", r.Id, r.UserId, r.CreatedAt.Unix(), r.ExpiresAt.Unix(), code)
	return encoded
}
