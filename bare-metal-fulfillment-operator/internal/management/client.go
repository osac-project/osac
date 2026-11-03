// Package management provides a backend-agnostic interface for bare metal power control.
package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

var ErrTransitioning = errors.New("power state transition already in progress")

type PowerState string

const (
	PowerOn  PowerState = "power on"
	PowerOff PowerState = "power off"
)

type PowerStatus struct {
	State           PowerState
	IsTransitioning bool
	FailureMessage  string
	// FailureMessageIsCurrent is true when the backend ties the message to the
	// current power state, rather than retaining it from a prior operation.
	FailureMessageIsCurrent bool
}

type Config struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Options map[string]any `json:"options"`
}

// HandoffObservation is the current step of a network-handoff reboot.
type HandoffObservation struct {
	// Confirmed is true when the handoff reboot has finished (annotation gone
	// and powered on for Metal3; powered on after PoweringOn for Ironic).
	Confirmed bool
	// Failed is true when the backend reports a failed power operation.
	Failed bool
	// Reason is the BMI NetworkHandoffComplete reason while not confirmed
	// (Progressing, PoweringOff, PoweringOn).
	Reason string
	// Message is a human-readable status for the BMI condition.
	Message string
}

type Client interface {
	GetPowerState(ctx context.Context, hostID string) (*PowerStatus, error)
	SetPowerState(ctx context.Context, hostID string, target PowerState) error
	TriggerRestart(ctx context.Context, hostID string) error
	IsRestartComplete(ctx context.Context, hostID string) (bool, error)

	// RequestNetworkHandoff starts the design Create-step-6 handoff reboot.
	// Metal3 sets online=true and reboot.metal3.io/osac-handoff={"mode":"hard"}.
	// Ironic begins a power-off. Idempotent when a handoff is already pending.
	RequestNetworkHandoff(ctx context.Context, hostID string) error
	// ObserveNetworkHandoff advances or confirms a pending handoff. currentReason
	// is the BMI condition reason. lastErrorFingerprint is the provider failure
	// message fingerprint captured before the handoff request.
	ObserveNetworkHandoff(ctx context.Context, hostID, currentReason, lastErrorFingerprint string) (HandoffObservation, error)
	// ClearPendingNetworkHandoff prepares the host for network offboarding.
	// Metal3 sets spec.online=false and removes any handoff annotation in one
	// patch so cancellation cannot boot a host held off by the annotation.
	// Safe when no handoff is pending.
	ClearPendingNetworkHandoff(ctx context.Context, hostID string) error
}

type NewClientFunc func(ctx context.Context, cfg *Config) (Client, error)

// FailureMessageFingerprint returns a stable, non-reversible fingerprint for
// comparing provider failure text across power-operation reconciles.
func FailureMessageFingerprint(message string) string {
	fingerprint := sha256.Sum256([]byte(message))
	return hex.EncodeToString(fingerprint[:])
}

var newClientFuncs = make(map[string]NewClientFunc)

func NewClient(ctx context.Context, cfg *Config) (Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("management backend configuration is required")
	}
	fn, ok := newClientFuncs[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported management backend type: %q", cfg.Type)
	}
	return fn(ctx, cfg)
}
