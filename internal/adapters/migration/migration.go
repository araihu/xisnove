// Package migration defines profile-neutral schema compatibility and migration
// failure contracts. Database adapters provide the locking and lease SQL.
package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Phase string

const (
	PhaseExpand   Phase = "expand"
	PhaseContract Phase = "contract"
)

type SchemaInterval struct {
	Minimum int64
	Maximum int64
}

func (i SchemaInterval) Contains(version int64) bool {
	return i.Minimum > 0 && i.Minimum <= i.Maximum && version >= i.Minimum && version <= i.Maximum
}

func (i SchemaInterval) Validate() error {
	if i.Minimum <= 0 || i.Maximum < i.Minimum {
		return fmt.Errorf("invalid supported schema interval [%d,%d]", i.Minimum, i.Maximum)
	}
	return nil
}

type Options struct {
	InstallationID string
	OwnerID        string
	LockTimeout    time.Duration
	PollInterval   time.Duration
	LeaseTTL       time.Duration
}

func DefaultOptions(ownerID string) Options {
	return Options{
		InstallationID: "default",
		OwnerID:        ownerID,
		LockTimeout:    30 * time.Second,
		PollInterval:   100 * time.Millisecond,
		LeaseTTL:       30 * time.Second,
	}
}

func (o Options) Validate() error {
	if strings.TrimSpace(o.InstallationID) == "" {
		return fmt.Errorf("migration installation ID is required")
	}
	if strings.TrimSpace(o.OwnerID) == "" {
		return fmt.Errorf("migration owner ID is required")
	}
	if o.LockTimeout <= 0 {
		return fmt.Errorf("migration lock timeout must be positive")
	}
	if o.PollInterval <= 0 || o.PollInterval > o.LockTimeout {
		return fmt.Errorf("migration poll interval must be positive and no greater than lock timeout")
	}
	if o.LeaseTTL < time.Millisecond || o.LeaseTTL < o.PollInterval {
		return fmt.Errorf("migration lease TTL must be at least the poll interval")
	}
	return nil
}

var (
	ErrContention              = errors.New("migration contention")
	ErrTimeout                 = errors.New("migration lock timeout")
	ErrIncompatibleSchema      = errors.New("incompatible schema")
	ErrLiveIncompatibleProcess = errors.New("live incompatible process")
)

type ErrorCode string

const (
	CodeContention              ErrorCode = "migration_contention"
	CodeTimeout                 ErrorCode = "migration_timeout"
	CodeIncompatibleSchema      ErrorCode = "schema_incompatible"
	CodeLiveIncompatibleProcess ErrorCode = "live_incompatible_process"
)

type classifiedError struct {
	code      ErrorCode
	retryable bool
	sentinel  error
	detail    string
}

func (e *classifiedError) Error() string { return string(e.code) + ": " + e.detail }
func (e *classifiedError) Unwrap() error { return e.sentinel }

func NewContentionError(detail string) error {
	return &classifiedError{code: CodeContention, retryable: true, sentinel: ErrContention, detail: detail}
}
func NewTimeoutError(detail string) error {
	return &classifiedError{code: CodeTimeout, retryable: true, sentinel: errors.Join(ErrTimeout, ErrContention), detail: detail}
}
func NewIncompatibleSchemaError(detail string) error {
	return &classifiedError{code: CodeIncompatibleSchema, sentinel: ErrIncompatibleSchema, detail: detail}
}
func NewLiveIncompatibleProcessError(detail string) error {
	return &classifiedError{code: CodeLiveIncompatibleProcess, sentinel: ErrLiveIncompatibleProcess, detail: detail}
}

func ClassifyLockError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewTimeoutError(err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return err
}

func Code(err error) ErrorCode {
	var classified *classifiedError
	if errors.As(err, &classified) {
		return classified.code
	}
	return ""
}

func Retryable(err error) bool {
	var classified *classifiedError
	return errors.As(err, &classified) && classified.retryable
}

type ProcessLease struct {
	InstallationID string
	ProcessID      string
	ProcessVersion string
	Readable       SchemaInterval
	TTL            time.Duration
}

func (l ProcessLease) Validate() error {
	if strings.TrimSpace(l.InstallationID) == "" {
		return fmt.Errorf("process lease installation ID is required")
	}
	if strings.TrimSpace(l.ProcessID) == "" {
		return fmt.Errorf("process lease process ID is required")
	}
	if strings.TrimSpace(l.ProcessVersion) == "" {
		return fmt.Errorf("process lease version is required")
	}
	if err := l.Readable.Validate(); err != nil {
		return err
	}
	if l.TTL < time.Millisecond {
		return fmt.Errorf("process lease TTL must be at least one millisecond")
	}
	return nil
}
