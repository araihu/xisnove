package seasonalassets

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestFrozenRuntimeMatchesAssetsV011(t *testing.T) {
	body := Runtime()
	if got, want := len(body), 21528; got != want {
		t.Fatalf("runtime size = %d, want %d", got, want)
	}
	if got := sha256Hex(body); got != RuntimeSHA256 {
		t.Fatalf("runtime SHA-256 = %s, want %s", got, RuntimeSHA256)
	}
	sha384 := sha512.Sum384(body)
	if got := "sha384-" + base64.StdEncoding.EncodeToString(sha384[:]); got != RuntimeIntegrity {
		t.Fatalf("runtime SRI = %s, want %s", got, RuntimeIntegrity)
	}

	body[0] ^= 0xff
	if got := sha256Hex(Runtime()); got != RuntimeSHA256 {
		t.Fatalf("Runtime returned mutable shared bytes: SHA-256 = %s", got)
	}
}

func TestDescriptorsMatchFrozenReleaseInventory(t *testing.T) {
	want := map[string]string{
		RuntimePath:       RuntimeSHA256,
		LogoPath:          "ae4da6e933399b7a6042aab91a1ceca9e7132c744a8717258815cd435282a2fb",
		SocialPreviewPath: SocialPreviewSHA256,
		MarkPath:          "98ac8f9069dddf94f06752fa2518d8987e45a747d2bc0c8a142d3a4f4fe52523",
		ReverseMarkPath:   "f3e0c0061ef495d62eaa7312cf85fdb4b4579f39094489ab7b806296a2cecda2",
		FaviconPath:       "98ac8f9069dddf94f06752fa2518d8987e45a747d2bc0c8a142d3a4f4fe52523",
	}

	descriptors := Descriptors()
	if got := len(descriptors); got != len(want) {
		t.Fatalf("descriptor count = %d, want %d", got, len(want))
	}
	for _, descriptor := range descriptors {
		asset, found := lookup(descriptor.Path)
		if !found {
			t.Errorf("descriptor %s has no staged body", descriptor.Path)
			continue
		}
		if got := sha256Hex(asset.body); got != want[descriptor.Path] || descriptor.SHA256 != got {
			t.Errorf("%s SHA-256 body=%s descriptor=%s inventory=%s", descriptor.Path, got, descriptor.SHA256, want[descriptor.Path])
		}
		delete(want, descriptor.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing descriptors: %v", want)
	}
}

func TestSocialPreviewIsVersionedLandscapeAsset(t *testing.T) {
	if len(socialPreview) > 1<<20 {
		t.Fatalf("social preview size = %d bytes, want <= 1 MiB", len(socialPreview))
	}

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://x9.araihu.com"+SocialPreviewPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("social preview status = %d, want %d", response.Code, http.StatusOK)
	}
	if got, want := response.Header().Get("Content-Type"), "image/png"; got != want {
		t.Fatalf("social preview Content-Type = %q, want %q", got, want)
	}
	config, err := png.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode social preview dimensions: %v", err)
	}
	if config.Width != 1280 || config.Height != 640 {
		t.Fatalf("social preview dimensions = %dx%d, want 1280x640", config.Width, config.Height)
	}
}

func TestHandlerServesOnlyStagedRoutes(t *testing.T) {
	handler := Handler()
	for _, descriptor := range Descriptors() {
		t.Run(descriptor.Path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://ui.example.test"+descriptor.Path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if got := response.Header().Get("Content-Type"); got != descriptor.ContentType {
				t.Errorf("Content-Type = %q, want %q", got, descriptor.ContentType)
			}
			if got := response.Header().Get("Cache-Control"); got != descriptor.CacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, descriptor.CacheControl)
			}
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := sha256Hex(response.Body.Bytes()); got != descriptor.SHA256 {
				t.Errorf("body SHA-256 = %s, want %s", got, descriptor.SHA256)
			}
			if got := response.Header().Get("Content-Length"); got != strconv.Itoa(response.Body.Len()) {
				t.Errorf("Content-Length = %q, want %d", got, response.Body.Len())
			}
		})
	}
}

func TestHandlerSupportsHeadAndRejectsMutation(t *testing.T) {
	handler := Handler()

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "https://ui.example.test"+RuntimePath, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "21528" {
		t.Fatalf("HEAD runtime = status %d, length %q, body %d", head.Code, head.Header().Get("Content-Length"), head.Body.Len())
	}

	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "https://ui.example.test"+RuntimePath, nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST runtime = status %d, Allow %q", post.Code, post.Header().Get("Allow"))
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "https://ui.example.test/assets/campaign/other.js", nil))
	if missing.Code != http.StatusNotFound {
		body, _ := io.ReadAll(missing.Result().Body)
		t.Fatalf("unknown route = status %d, body %q", missing.Code, body)
	}
}

func sha256Hex(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
