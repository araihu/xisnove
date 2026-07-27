package migration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchemaIntervalContainsOnlyBoundedVersions(t *testing.T) {
	t.Parallel()
	interval := SchemaInterval{Minimum: 10, Maximum: 11}
	for _, test := range []struct {
		version int64
		want    bool
	}{{9, false}, {10, true}, {11, true}, {12, false}} {
		if got := interval.Contains(test.version); got != test.want {
			t.Fatalf("Contains(%d) = %v, want %v", test.version, got, test.want)
		}
	}
}

func TestOptionsRejectUnboundedOrInvalidClockValues(t *testing.T) {
	t.Parallel()
	for _, options := range []Options{
		{},
		{OwnerID: "owner", LockTimeout: time.Second, PollInterval: time.Second, LeaseTTL: time.Second},
		{OwnerID: "owner", InstallationID: "installation", LockTimeout: time.Second, PollInterval: 2 * time.Second, LeaseTTL: time.Second},
	} {
		if err := options.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil", options)
		}
	}
}

func TestClassifyContextDeadlineAsStableTimeout(t *testing.T) {
	t.Parallel()
	err := ClassifyLockError(context.DeadlineExceeded)
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrContention) || !Retryable(err) || Code(err) != CodeTimeout {
		t.Fatalf("classified error = %v, code = %q", err, Code(err))
	}
}

func TestContentionErrorHasStableRetryableClass(t *testing.T) {
	t.Parallel()
	err := NewContentionError("migration lease held")
	if !errors.Is(err, ErrContention) || !Retryable(err) || Code(err) != CodeContention {
		t.Fatalf("contention error = %v, retryable=%v, code=%q", err, Retryable(err), Code(err))
	}
}

func TestPhasePlanRequiresExplicitExpandAndContractTargets(t *testing.T) {
	t.Parallel()
	plan := PhasePlan{ExpandThrough: 11, ContractThrough: 12}
	if err := plan.Validate(12); err != nil {
		t.Fatal(err)
	}
	if got := plan.Target(PhaseExpand); got != 11 {
		t.Fatalf("expand target = %d, want 11", got)
	}
	if got := plan.Target(PhaseContract); got != 12 {
		t.Fatalf("contract target = %d, want 12", got)
	}
	for _, invalid := range []PhasePlan{
		{},
		{ExpandThrough: 12, ContractThrough: 11},
		{ExpandThrough: 11, ContractThrough: 11},
	} {
		if err := invalid.Validate(12); err == nil {
			t.Fatalf("Validate(%+v) error = nil", invalid)
		}
	}
}
