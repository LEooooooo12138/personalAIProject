package console

import (
	"errors"
	"time"
)

var (
	ErrUninitialized   = errors.New("console uninitialized")
	ErrUnauthenticated = errors.New("console unauthenticated")
	ErrForbidden       = errors.New("console forbidden")
	ErrConflict        = errors.New("console conflict")
	ErrInvalid         = errors.New("console invalid input")
	ErrUnavailable     = errors.New("console unavailable")
	ErrBusy            = errors.New("console busy")
)

type User struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Role               string `json:"role"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"must_change_password"`
}

type Principal struct {
	UserID             string `json:"user_id"`
	Role               string `json:"role"`
	SessionID          string `json:"session_id"`
	MustChangePassword bool   `json:"must_change_password"`
}

type Login struct {
	Token     string    `json:"token"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
	Principal Principal `json:"principal"`
}

type Sharing struct {
	Revision      uint64   `json:"revision"`
	EntityIDs     []string `json:"entity_ids"`
	SuggestionIDs []string `json:"suggestion_ids"`
}
