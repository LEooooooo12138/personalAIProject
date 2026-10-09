package smarthome

import (
	"context"
	"errors"
	"time"
)

type ControlActor struct{ UserID, SessionID string }
type ControlIntent struct {
	InputHash string `json:"input_hash,omitempty"`
	Kind      string `json:"kind"`
	EntityID  string `json:"entity_id"`
	Action    string `json:"action"`
}
type QueryTarget struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
	AreaName string `json:"area_name"`
	Domain   string `json:"domain"`
}
type ControlTarget struct {
	EntityID             string   `json:"entity_id" yaml:"entity_id"`
	Name                 string   `json:"name" yaml:"name"`
	AreaName             string   `json:"area_name" yaml:"area_name"`
	Domain               string   `json:"domain" yaml:"domain"`
	Aliases              []string `json:"aliases,omitempty" yaml:"aliases"`
	AllowedActions       []string `json:"allowed_actions" yaml:"allowed_actions"`
	LoadLocationVerified bool     `json:"load_location_verified" yaml:"load_location_verified"`
}
type ControlPolicy struct {
	Revision string
	Targets  []ControlTarget
}
type ControlConfig struct {
	Targets                                                   []ControlTarget
	ProposalTTL, ReadbackTimeout, ReadbackInterval, Retention time.Duration
	MaxRecords                                                int
}

func DefaultControlConfig() ControlConfig {
	return ControlConfig{ProposalTTL: 120 * time.Second, ReadbackTimeout: 10 * time.Second, ReadbackInterval: 500 * time.Millisecond, Retention: 30 * 24 * time.Hour, MaxRecords: 10000}
}

type DeviceQueryResult struct {
	EntityID   string    `json:"entity_id"`
	Name       string    `json:"name"`
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observed_at"`
}
type ControlProposal struct {
	ID             string             `json:"id"`
	OwnerUserID    string             `json:"-"`
	SessionID      string             `json:"session_id"`
	RequestID      string             `json:"request_id"`
	EntityID       string             `json:"entity_id"`
	Name           string             `json:"name"`
	AreaName       string             `json:"area_name"`
	Action         string             `json:"action"`
	PolicyRevision string             `json:"policy_revision"`
	Status         string             `json:"status"`
	CreatedAt      time.Time          `json:"created_at"`
	ExpiresAt      time.Time          `json:"expires_at"`
	Before         *DeviceQueryResult `json:"before,omitempty"`
	After          *DeviceQueryResult `json:"after,omitempty"`
	ErrorCode      string             `json:"error_code,omitempty"`
}
type ProposalOutcome struct {
	Kind         string             `json:"kind"`
	Proposal     *ControlProposal   `json:"proposal,omitempty"`
	DeviceResult *DeviceQueryResult `json:"device_result,omitempty"`
}
type ControlHAClient interface {
	GetState(context.Context, string) (*EntityState, error)
	CallService(context.Context, string, string, map[string]interface{}) error
}
type ControlCatalogProvider interface {
	Get(context.Context) (CatalogSnapshot, error)
}

var (
	ErrControlInvalid     = errors.New("control_invalid")
	ErrControlForbidden   = errors.New("control_forbidden")
	ErrControlNotFound    = errors.New("control_not_found")
	ErrControlConflict    = errors.New("control_conflict")
	ErrControlExpired     = errors.New("control_expired")
	ErrControlUnsupported = errors.New("control_unsupported")
	ErrControlUnavailable = errors.New("control_unavailable")
	ErrControlCapacity    = errors.New("control_capacity")
)

func ControlErrorCode(e error) string {
	for _, v := range []error{ErrControlInvalid, ErrControlForbidden, ErrControlNotFound, ErrControlConflict, ErrControlExpired, ErrControlUnsupported, ErrControlUnavailable, ErrControlCapacity} {
		if errors.Is(e, v) {
			return v.Error()
		}
	}
	return "control_unavailable"
}

type controlAuthorizationKey struct{}

// WithControlAuthorization binds a fresh Console identity check to this request only.
func WithControlAuthorization(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, controlAuthorizationKey{}, check)
}
func authorizeControl(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	check, ok := ctx.Value(controlAuthorizationKey{}).(func(context.Context) error)
	if !ok || check == nil || check(ctx) != nil {
		return ErrControlForbidden
	}
	return ctx.Err()
}

type ControlOption func(*ControlService)

func WithControlClock(now func() time.Time, wait func(context.Context, time.Duration) error) ControlOption {
	return func(s *ControlService) {
		if now != nil {
			s.now = now
		}
		if wait != nil {
			s.wait = wait
		}
	}
}
