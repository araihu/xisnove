// Package shoutrrr implements the reviewed Shoutrrr transport subset.
package shoutrrr

import (
	"slices"
	"time"

	libshoutrrr "github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// Compile-time guards make upgrades fail visibly if the injected HTTP client
// or sender option API audited for v0.16.2 changes.
type senderFactory func(types.SenderOptions, ...string) (*router.ServiceRouter, error)

var (
	_ senderFactory = libshoutrrr.CreateSenderWithOptions
	_               = types.SenderOptions{HTTPClient: nil, Timeout: time.Second}
)

// reviewedHTTPSchemes are the v0.16.2 services whose implementations expose
// types.HTTPClientSetter and therefore accept Xisnove's context- and
// egress-enforcing client. Network services outside this list are rejected.
var reviewedHTTPSchemes = []string{
	"bark", "discord", "generic", "gotify", "googlechat", "hangouts",
	"ifttt", "join", "lark", "matrix", "mattermost", "notifiarr", "ntfy",
	"opsgenie", "pagerduty", "pushbullet", "pushover", "rocketchat", "signal",
	"slack", "teams", "telegram", "twilio", "wecom", "zulip",
}

// SchemeReviewed reports whether v0.16.2 accepts Xisnove's injected HTTP
// client for the scheme.
func SchemeReviewed(scheme string) bool {
	return slices.Contains(reviewedHTTPSchemes, scheme)
}

// ReviewedSchemes returns a defensive copy of the audited v0.16.2 subset.
func ReviewedSchemes() []string {
	return slices.Clone(reviewedHTTPSchemes)
}
