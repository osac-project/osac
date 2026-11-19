// Package management provides a backend-agnostic interface for bare metal power control.
package management

import (
	"context"
	"errors"
	"fmt"
)

var ErrTransitioning = errors.New("power state transition already in progress")

// ErrPowerRead marks a transient power-state read failure. The caller must not
// treat it as an observed power transition.
var ErrPowerRead = errors.New("power state read failed")

// ErrHostOperational marks a terminal host operational error, as opposed to a
// transient backend or API failure.
var ErrHostOperational = errors.New("host operational error")

type PowerState string

const (
	PowerOn  PowerState = "power on"
	PowerOff PowerState = "power off"
)

type PowerStatus struct {
	State           PowerState
	IsTransitioning bool
}

type Config struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Options map[string]any `json:"options"`
}

type Client interface {
	GetPowerState(ctx context.Context, hostID string) (*PowerStatus, error)
	SetPowerState(ctx context.Context, hostID string, target PowerState) error
	TriggerRestart(ctx context.Context, hostID, requestID string) error
	IsRestartComplete(ctx context.Context, hostID string) (bool, error)
}

type NewClientFunc func(ctx context.Context, cfg *Config) (Client, error)

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
