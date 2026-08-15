// Package seasonalassets owns the frozen Arai Hu Assets v0.1.1 campaign
// runtime and X-9 baseline assets served by the UI's explicit immutable
// seasonal routes.
package seasonalassets

import (
	"bytes"
	_ "embed"
	"net/http"
	"strconv"
)

const (
	SourceRelease = "v0.1.1"
	SourceCommit  = "74c36ed038ad127cab72d10ac6c5a8ca79646244"

	RuntimePath       = "/assets/campaign/v1.js"
	RuntimeSourcePath = "campaign/v1.js"
	RuntimeSHA256     = "a936193b4fed8120e6cb3423f19d3e2ddb0ba32266dc4e5f02a98f5261853709"
	RuntimeIntegrity  = "sha384-oPH7l1vK9vKP1Dn+18sO3yEXlz4ts6KzPEQl0SW4Y/+im05gOaamNNaQAf6bGH/n"
	ChannelURL        = "https://araihu.com/assets/releases/current"

	LogoPath        = "/ui/seasonal/v0.1.1/x9-logo.svg"
	MarkPath        = "/ui/seasonal/v0.1.1/x9-mark.svg"
	ReverseMarkPath = "/ui/seasonal/v0.1.1/x9-mark-reverse.svg"
	FaviconPath     = "/ui/seasonal/v0.1.1/x9-favicon.svg"

	runtimeCache   = "public, max-age=300"
	immutableCache = "public, max-age=31536000, immutable"
)

//go:embed static/campaign-v1.js
var runtime []byte

//go:embed static/x9-logo.svg
var logo []byte

//go:embed static/x9-mark.svg
var mark []byte

//go:embed static/x9-mark-reverse.svg
var reverseMark []byte

//go:embed static/x9-favicon.svg
var favicon []byte

// Descriptor records the immutable upstream identity and same-origin route for
// one staged asset. Body bytes remain private and cannot be mutated by callers.
type Descriptor struct {
	Path         string
	SourcePath   string
	SHA256       string
	ContentType  string
	CacheControl string
}

type stagedAsset struct {
	descriptor Descriptor
	body       []byte
}

var staged = []stagedAsset{
	{
		descriptor: Descriptor{
			Path:         RuntimePath,
			SourcePath:   RuntimeSourcePath,
			SHA256:       RuntimeSHA256,
			ContentType:  "text/javascript; charset=utf-8",
			CacheControl: runtimeCache,
		},
		body: runtime,
	},
	{
		descriptor: Descriptor{
			Path:         LogoPath,
			SourcePath:   "brand/x9/logo/adaptive-transparent-optical.svg",
			SHA256:       "ae4da6e933399b7a6042aab91a1ceca9e7132c744a8717258815cd435282a2fb",
			ContentType:  "image/svg+xml",
			CacheControl: immutableCache,
		},
		body: logo,
	},
	{
		descriptor: Descriptor{
			Path:         MarkPath,
			SourcePath:   "icons/brand/x9-icon-adaptive-transparent-optical.svg",
			SHA256:       "98ac8f9069dddf94f06752fa2518d8987e45a747d2bc0c8a142d3a4f4fe52523",
			ContentType:  "image/svg+xml",
			CacheControl: immutableCache,
		},
		body: mark,
	},
	{
		descriptor: Descriptor{
			Path:         ReverseMarkPath,
			SourcePath:   "icons/brand/x9-icon-light-transparent-optical.svg",
			SHA256:       "f3e0c0061ef495d62eaa7312cf85fdb4b4579f39094489ab7b806296a2cecda2",
			ContentType:  "image/svg+xml",
			CacheControl: immutableCache,
		},
		body: reverseMark,
	},
	{
		descriptor: Descriptor{
			Path:         FaviconPath,
			SourcePath:   "platform/web/x9/favicon.svg",
			SHA256:       "98ac8f9069dddf94f06752fa2518d8987e45a747d2bc0c8a142d3a4f4fe52523",
			ContentType:  "image/svg+xml",
			CacheControl: immutableCache,
		},
		body: favicon,
	},
}

// Descriptors returns the stable route and provenance plan. Returned values
// contain no mutable asset bytes.
func Descriptors() []Descriptor {
	descriptors := make([]Descriptor, len(staged))
	for index := range staged {
		descriptors[index] = staged[index].descriptor
	}
	return descriptors
}

// Runtime returns a defensive copy of the frozen runtime.
func Runtime() []byte {
	return bytes.Clone(runtime)
}

// Handler serves only the staged seasonal routes. The UI server mounts it
// explicitly so the asset namespace remains easy to audit.
func Handler() http.Handler {
	return http.HandlerFunc(serve)
}

func serve(w http.ResponseWriter, r *http.Request) {
	asset, found := lookup(r.URL.Path)
	if !found {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", asset.descriptor.ContentType)
	w.Header().Set("Cache-Control", asset.descriptor.CacheControl)
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.body)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(asset.body)
}

func lookup(path string) (stagedAsset, bool) {
	for _, asset := range staged {
		if asset.descriptor.Path == path {
			return asset, true
		}
	}
	return stagedAsset{}, false
}
