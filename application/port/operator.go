package port

import (
	"context"
	"time"
)

// ExternalOwner identifies an externally managed object without coupling the
// core to any particular orchestration API. Key is its stable name and UID is
// its incarnation identity.
type ExternalOwner struct {
	Key string
	UID string
}

type OperatorBinding struct {
	Owner      ExternalOwner
	Kind       string
	ResourceID string
	DeletedAt  *time.Time
}

type OperatorRepository interface {
	Resolve(context.Context, ExternalOwner, string) (OperatorBinding, error)
	Bind(context.Context, OperatorBinding) error
	Tombstone(context.Context, ExternalOwner, string, time.Time) error
}
