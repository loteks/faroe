package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"faroe/ratelimit"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "embed"

	"github.com/julienschmidt/httprouter"
	_ "github.com/mattn/go-sqlite3"
)

const version = "0.1.0"

var secret []byte

var db *sql.DB

var passwordHashingIPRateLimit = ratelimit.NewTokenBucketRateLimit(5, 10*time.Second)

var loginIPRateLimit = ratelimit.NewExpiringTokenBucketRateLimit(5, 15*time.Minute)

var createEmailVerificationUserRateLimit = ratelimit.NewTokenBucketRateLimit(3, 5*time.Minute)

var verifyUserEmailRateLimit = ratelimit.NewExpiringTokenBucketRateLimit(5, 15*time.Minute)

var verifyEmailUpdateVerificationCodeLimitCounter = ratelimit.NewLimitCounter(5)

var createPasswordResetIPRateLimit = ratelimit.NewTokenBucketRateLimit(3, 5*time.Minute)

var verifyPasswordResetCodeLimitCounter = ratelimit.NewLimitCounter(5)

var totpUserRateLimit = ratelimit.NewExpiringTokenBucketRateLimit(5, 15*time.Minute)

var recoveryCodeUserRateLimit = ratelimit.NewExpiringTokenBucketRateLimit(5, 15*time.Minute)

//go:embed schema.sql
var schema string

func main() {
	for _, arg := range os.Args {
		if arg == "-v" || arg == "--version" {
			fmt.Printf("Faroe version %s\n", version)
			return
		}
	}

	if len(os.Args) < 2 {
		fmt.Print(`Usage:

faroe serve - Start the Faroe server
faroe generate-secret - Generate a secure secret

`)
		return
	}

	if os.Args[1] == "serve" {
		serveCommand()
		return
	}
	if os.Args[1] == "generate-secret" {
		generateSecretCommand()
		return
	}

	fmt.Println("Unknown command")
}

func generateSecretCommand() {
	bytes := make([]byte, 25)
	_, err := rand.Read(bytes)
	if err != nil {
		log.Fatal("Failed to generate secret\n")
	}
	secret := base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").EncodeToString(bytes)
	fmt.Println(secret)
}

func serveCommand() {
	// Remove "server" command since Go's flag package stops parsing at first non-flag argument.
	os.Args = os.Args[1:]

	var port int
	var secretString string
	flag.IntVar(&port, "port", 3000, "Port number")
	flag.StringVar(&secretString, "secret", "", "Server secret")
	flag.Parse()

	secret = []byte(secretString)

	go func() {
		for range time.Tick(10 * 24 * time.Hour) {
			passwordHashingIPRateLimit.Clear()
			loginIPRateLimit.Clear()
			createEmailVerificationUserRateLimit.Clear()
			verifyUserEmailRateLimit.Clear()
			verifyEmailUpdateVerificationCodeLimitCounter.Clear()
			createPasswordResetIPRateLimit.Clear()
			verifyPasswordResetCodeLimitCounter.Clear()
			totpUserRateLimit.Clear()
			log.Println("SYSTEM RESET_MEMORY_STORAGE")
		}
	}()

	err := os.MkdirAll("faroe_data", os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}

	db, err = sql.Open("sqlite3", "./faroe_data/sqlite.db?_journal_mode=WAL")
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(schema)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for range time.Tick(10 * 24 * time.Hour) {
			err := cleanUpDatabase()
			if err != nil {
				log.Println(err)
			}
		}
	}()

	go func() {
		// for range time.Tick(1 * time.Hour) {
		// 	err = backupDatabase()
		// 	if err != nil {
		// 		log.Printf("SYSTEM DATABASE_BACKUP %e\n", err)
		// 	} else {
		// 		log.Println("SYSTEM DATABASE_BACKUP SUCCESS")
		// 	}
		// }
	}()

	router := &httprouter.Router{
		NotFound: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !verifySecret(r) {
				writeNotAuthenticatedErrorResponse(w)
			} else {
				writeNotFoundErrorResponse(w)
			}
		}),
	}

	router.GET("/", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		if !verifySecret(r) {
			writeNotAuthenticatedErrorResponse(w)
			return
		}
		w.Write([]byte(fmt.Sprintf("Faroe version %s\n\nRead the documentation: https://faroe.dev\n", version)))
	})

	router.POST("/authenticate/password", handleAuthenticateWithPasswordRequest)

	router.POST("/users", handleCreateUserRequest)
	router.GET("/users", handleGetUsersRequest)
	router.DELETE("/users", handleDeleteUsersRequest)
	router.GET("/users/:user_id", handleGetUserRequest)
	router.DELETE("/users/:user_id", handleDeleteUserRequest)
	router.POST("/users/:user_id/update-password", handleUpdateUserPasswordRequest)
	router.GET("/users/:user_id/totp", handleGetUserTOTPCredentialRequest)
	router.POST("/users/:user_id/totp", handleRegisterTOTPRequest)
	router.DELETE("/users/:user_id/totp", handleDeleteUserTOTPCredentialRequest)
	router.GET("/users/:user_id/recovery-code", handleGetUserRecoveryCodeRequest)
	router.POST("/users/:user_id/verify-2fa/totp", handleVerifyTOTPRequest)
	router.POST("/users/:user_id/reset-2fa", handleResetUser2FARequest)
	router.POST("/users/:user_id/regenerate-recovery-code", handleRegenerateUserRecoveryCodeRequest)
	router.POST("/users/:user_id/email-verification-request", handleCreateUserEmailVerificationRequestRequest)
	router.GET("/users/:user_id/email-verification-request", handleGetUserEmailVerificationRequestRequest)
	router.DELETE("/users/:user_id/email-verification-request", handleDeleteUserEmailVerificationRequestRequest)
	router.POST("/users/:user_id/verify-email", handleVerifyUserEmailRequest)
	router.POST("/users/:user_id/email-update-requests", handleCreateUserEmailUpdateRequestRequest)

	router.GET("/email-update-requests/:request_id", handleGetEmailUpdateRequestRequest)
	router.DELETE("/email-update-requests/:request_id", handleDeleteEmailUpdateRequestRequest)
	router.POST("/update-email", handleUpdateEmailRequest)

	router.POST("/password-reset-requests", handleCreatePasswordResetRequestRequest)
	router.GET("/password-reset-requests/:request_id", handleGetPasswordResetRequestRequest)
	router.DELETE("/password-reset-requests/:request_id", handleDeletePasswordResetRequestRequest)
	router.POST("/password-reset-requests/:request_id/verify-email", handleVerifyPasswordResetRequestEmailRequest)
	router.POST("/reset-password", handleResetPasswordRequest)

	fmt.Printf("Starting server in port %d...\n", port)
	err = http.ListenAndServe(fmt.Sprintf(":%d", port), router)
	log.Println(err)
}

func writeExpectedErrorResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	escapedMessage := strings.ReplaceAll(string(message), "\"", "\\\"")
	w.Write([]byte(fmt.Sprintf("{\"error\": \"%s\"}", escapedMessage)))
}

func writeUnExpectedErrorResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(500)
	w.Write([]byte("{\"error\":\"UNEXPECTED_ERROR\"}"))
}

func writeNotFoundErrorResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(404)
	w.Write([]byte("{\"error\":\"NOT_FOUND\"}"))
}

func writeNotAcceptableErrorResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(406)
	w.Write([]byte("{\"error\":\"NOT_ACCEPTABLE\"}"))
}

func writeUnsupportedMediaTypeErrorResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(415)
	w.Write([]byte("{\"error\":\"UNSUPPORTED_MEDIA_TYPE\"}"))
}

func writeNotAuthenticatedErrorResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(401)
	w.Write([]byte("{\"error\":\"NOT_AUTHENTICATED\"}"))
}

func logMessageWithClientIP(level string, action string, result string, clientIP string, attributes string) {
	if clientIP == "" {
		clientIP = "-"
	}
	log.Printf("%s %s %s client_ip=%s %s\n", level, action, result, clientIP, attributes)
}

func generateId() (string, error) {
	bytes := make([]byte, 15)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	id := base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").EncodeToString(bytes)
	return id, nil
}

type SortOrder int

const (
	SortOrderAscending SortOrder = iota
	SortOrderDescending
)

const (
	ExpectedErrorInvalidData             = "INVALID_DATA"
	ExpectedErrorTooManyRequests         = "TOO_MANY_REQUESTS"
	ExpectedErrorWeakPassword            = "WEAK_PASSWORD"
	ExpectedErrorEmailAlreadyUsed        = "EMAIL_ALREADY_USED"
	ExpectedErrorUserNotExists           = "USER_NOT_EXISTS"
	ExpectedErrorIncorrectPassword       = "INCORRECT_PASSWORD"
	ExpectedErrorIncorrectCode           = "INCORRECT_CODE"
	ExpectedErrorSecondFactorNotVerified = "SECOND_FACTOR_NOT_VERIFIED"
	ExpectedErrorInvalidRequest          = "INVALID_REQUEST"
	ExpectedErrorNotAllowed              = "NOT_ALLOWED"
)

var ErrRecordNotFound = errors.New("record not found")
