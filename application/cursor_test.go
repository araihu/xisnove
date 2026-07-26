package application_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application"
)

func TestCursorRoundTripAndTamperRejection(t *testing.T) {
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	want := application.CursorKey{Sort: "2026-07-26T12:00:00.123Z", ID: "00000000-0000-4000-8000-000000000001"}
	token, err := codec.Encode(want, application.CursorSortTime)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, want.Sort) || strings.Contains(token, want.ID) {
		t.Fatal("cursor exposes plaintext key material")
	}
	got, err := codec.Decode(token, application.CursorSortTime)
	if err != nil || got != want {
		t.Fatalf("Decode() = %#v, %v", got, err)
	}

	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-2] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(payload) + "." + parts[1]
	assertInvalidCursor(t, codec, tampered, application.CursorSortTime)
	assertInvalidCursor(t, codec, "not-a-cursor", application.CursorSortTime)
}

type legacyOnlyCursorCodec struct{}

func (legacyOnlyCursorCodec) Encode(application.CursorKey, application.CursorSortKind) (string, error) {
	return "", nil
}

func (legacyOnlyCursorCodec) Decode(string, application.CursorSortKind) (application.CursorKey, error) {
	return application.CursorKey{}, nil
}

func TestLegacyCursorCodecRemainsImplementableByExternalFakes(t *testing.T) {
	var codec application.CursorCodec = legacyOnlyCursorCodec{}
	if codec == nil {
		t.Fatal("legacy fake was not assigned to CursorCodec")
	}
}

func TestNewHMACCursorCodecExposesAudienceExtension(t *testing.T) {
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	var audienceCodec application.AudienceCursorCodec = codec
	if audienceCodec == nil {
		t.Fatal("NewHMACCursorCodec() returned no audience codec")
	}
}

func TestAudienceCursorBindsEndpointAndFilters(t *testing.T) {
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	audience := application.CursorAudience{
		Endpoint: "/v1/incidents",
		Filter: map[string][]string{
			"monitorId": {"00000000-0000-4000-8000-000000000001"},
			"state":     {"open", "acknowledged"},
		},
	}
	key := application.CursorKey{Sort: "open", ID: "00000000-0000-4000-8000-000000000001"}
	token, err := codec.EncodeFor(audience, key, application.CursorShapeString)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, audience.Endpoint) || strings.Contains(token, key.Sort) || strings.Contains(token, key.ID) {
		t.Fatal("cursor exposes plaintext audience or key material")
	}
	if got, err := codec.DecodeFor(token, audience, application.CursorShapeString); err != nil || got != key {
		t.Fatalf("DecodeFor() = %#v, %v", got, err)
	}

	canonicalEquivalent := application.CursorAudience{
		Endpoint: "/v1/incidents",
		Filter: map[string][]string{
			"state":     {"acknowledged", "open"},
			"monitorId": {"00000000-0000-4000-8000-000000000001"},
		},
	}
	canonicalToken, err := codec.EncodeFor(canonicalEquivalent, key, application.CursorShapeString)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalToken != token {
		t.Fatalf("equivalent audience token = %q, want canonical %q", canonicalToken, token)
	}

	assertInvalidAudienceCursor(t, codec, token, application.CursorAudience{Endpoint: "/v1/incidents/events", Filter: audience.Filter}, application.CursorShapeString)
	assertInvalidAudienceCursor(t, codec, token, application.CursorAudience{Endpoint: audience.Endpoint, Filter: map[string][]string{"state": {"closed"}}}, application.CursorShapeString)

	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-2] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(payload) + "." + parts[1]
	assertInvalidAudienceCursor(t, codec, tampered, audience, application.CursorShapeString)
}

func TestAudienceCursorSupportsCanonicalIntegerAndTimeShapes(t *testing.T) {
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	audience := application.CursorAudience{Endpoint: "/v1/incidents", Filter: map[string][]string{"state": {"open"}}}
	id := "00000000-0000-4000-8000-000000000001"
	for _, test := range []struct {
		name  string
		shape application.CursorShape
		sort  string
	}{
		{name: "integer", shape: application.CursorShapeInt, sort: "42"},
		{name: "time", shape: application.CursorShapeTime, sort: "2026-07-26T12:00:00.123Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := application.CursorKey{Sort: test.sort, ID: id}
			token, err := codec.EncodeFor(audience, want, test.shape)
			if err != nil {
				t.Fatal(err)
			}
			got, err := codec.DecodeFor(token, audience, test.shape)
			if err != nil || got != want {
				t.Fatalf("DecodeFor() = %#v, %v", got, err)
			}
		})
	}
	for _, badInteger := range []string{"0042", "+42", "42.0"} {
		if _, err := codec.EncodeFor(audience, application.CursorKey{Sort: badInteger, ID: id}, application.CursorShapeInt); err == nil {
			t.Errorf("EncodeFor() accepted non-canonical integer %q", badInteger)
		}
	}
	if _, err := codec.EncodeFor(audience, application.CursorKey{Sort: "2026-07-26T09:00:00-03:00", ID: id}, application.CursorShapeTime); err == nil {
		t.Fatal("EncodeFor() accepted non-canonical UTC time")
	}
	if _, err := codec.EncodeFor(application.CursorAudience{Endpoint: "/v1/incidents/", Filter: audience.Filter}, application.CursorKey{Sort: "open", ID: id}, application.CursorShapeString); err == nil {
		t.Fatal("EncodeFor() accepted a non-canonical endpoint path")
	}
}

func TestCursorRejectsVersionAndSortMismatch(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	codec, err := application.NewHMACCursorCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	stringToken, err := codec.Encode(application.CursorKey{
		Sort: "gateway", ID: "00000000-0000-4000-8000-000000000001",
	}, application.CursorSortString)
	if err != nil {
		t.Fatal(err)
	}
	assertInvalidCursor(t, codec, stringToken, application.CursorSortTime)
	timeToken, err := codec.Encode(application.CursorKey{
		Sort: "2026-07-26T12:00:00Z", ID: "00000000-0000-4000-8000-000000000001",
	}, application.CursorSortTime)
	if err != nil {
		t.Fatal(err)
	}
	assertInvalidCursor(t, codec, timeToken, application.CursorSortString)
	assertInvalidCursor(t, codec, signedCursorToken(key, application.CursorSortString, `{"v":2,"sort":"gateway","id":"00000000-0000-4000-8000-000000000001"}`), application.CursorSortString)

	if _, err := codec.Encode(application.CursorKey{Sort: "2026-07-26T09:00:00-03:00", ID: "00000000-0000-4000-8000-000000000001"}, application.CursorSortTime); err == nil {
		t.Fatal("Encode() accepted non-canonical UTC sort")
	}
	if _, err := codec.Encode(application.CursorKey{Sort: "gateway", ID: "not-a-uuid"}, application.CursorSortString); err == nil {
		t.Fatal("Encode() accepted invalid UUID")
	}
	if _, err := application.NewHMACCursorCodec([]byte("short")); err == nil {
		t.Fatal("NewHMACCursorCodec() accepted a weak key")
	}
}

func TestCursorRejectsSignedTrailingData(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	codec, err := application.NewHMACCursorCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	token := signedCursorToken(key, application.CursorSortString, `{"v":1,"sort":"gateway","id":"00000000-0000-4000-8000-000000000001"} trailing`)
	assertInvalidCursor(t, codec, token, application.CursorSortString)
}

func TestPageLimitDefaultsTo50AndCapsAt200(t *testing.T) {
	for input, want := range map[int]int{-1: 50, 0: 50, 1: 1, 200: 200, 201: 200, 10_000: 200} {
		if got := application.NormalizePageLimit(input); got != want {
			t.Errorf("NormalizePageLimit(%d) = %d, want %d", input, got, want)
		}
	}
}

func assertInvalidCursor(t *testing.T, codec application.CursorCodec, token string, sort application.CursorSortKind) {
	t.Helper()
	_, err := codec.Decode(token, sort)
	var validation *application.ValidationError
	if !errors.As(err, &validation) || validation.Fields["cursor"] != "is invalid" {
		t.Fatalf("Decode() error = %v, want cursor ValidationError", err)
	}
}

func assertInvalidAudienceCursor(t *testing.T, codec application.AudienceCursorCodec, token string, audience application.CursorAudience, shape application.CursorShape) {
	t.Helper()
	_, err := codec.DecodeFor(token, audience, shape)
	var validation *application.ValidationError
	if !errors.As(err, &validation) || validation.Fields["cursor"] != "is invalid" {
		t.Fatalf("DecodeFor() error = %v, want cursor ValidationError", err)
	}
}

func signedCursorToken(key []byte, sortKind application.CursorSortKind, payload string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(sortKind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
