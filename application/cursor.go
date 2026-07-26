package application

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const cursorVersion = 1

const audienceCursorVersion = 2

type CursorShape string

const (
	CursorShapeString CursorShape = "string"
	CursorShapeInt    CursorShape = "int"
	CursorShapeTime   CursorShape = "time"
)

// CursorSortKind is retained for the first cursor format. New endpoints use
// CursorShape with an explicit CursorAudience.
type CursorSortKind = CursorShape

const (
	CursorSortString = CursorShapeString
	CursorSortTime   = CursorShapeTime
)

type CursorKey struct {
	Sort string
	ID   string
}

// CursorAudience binds a cursor to one endpoint and its complete filter set.
// Filter values are treated as unordered sets and serialized canonically.
type CursorAudience struct {
	Endpoint string
	Filter   map[string][]string
}

type CursorCodec interface {
	Encode(CursorKey, CursorSortKind) (string, error)
	Decode(string, CursorSortKind) (CursorKey, error)
	EncodeFor(CursorAudience, CursorKey, CursorShape) (string, error)
	DecodeFor(string, CursorAudience, CursorShape) (CursorKey, error)
}

type hmacCursorCodec struct {
	key []byte
}

type cursorPayload struct {
	Version int    `json:"v"`
	Sort    string `json:"sort"`
	ID      string `json:"id"`
}

type audienceCursorPayload struct {
	Version  int            `json:"v"`
	Endpoint string         `json:"endpoint"`
	Filter   []cursorFilter `json:"filter,omitempty"`
	Shape    CursorShape    `json:"shape"`
	Sort     string         `json:"sort"`
	ID       string         `json:"id"`
}

type cursorFilter struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

func NewHMACCursorCodec(key []byte) (CursorCodec, error) {
	if len(key) < sha256.Size {
		return nil, errors.New("cursor key must contain at least 32 bytes")
	}
	return &hmacCursorCodec{key: bytes.Clone(key)}, nil
}

func (c *hmacCursorCodec) Encode(key CursorKey, sortKind CursorSortKind) (string, error) {
	if !validCursorKey(key, sortKind) {
		return "", invalidCursorError()
	}
	payload, err := json.Marshal(cursorPayload{Version: cursorVersion, Sort: key.Sort, ID: key.ID})
	if err != nil {
		return "", invalidCursorError()
	}
	signature := c.sign(sortKind, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *hmacCursorCodec) Decode(value string, sortKind CursorSortKind) (CursorKey, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return CursorKey{}, invalidCursorError()
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return CursorKey{}, invalidCursorError()
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size || !hmac.Equal(signature, c.sign(sortKind, payload)) {
		return CursorKey{}, invalidCursorError()
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded cursorPayload
	if err := decoder.Decode(&decoded); err != nil {
		return CursorKey{}, invalidCursorError()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CursorKey{}, invalidCursorError()
	}
	key := CursorKey{Sort: decoded.Sort, ID: decoded.ID}
	if decoded.Version != cursorVersion || !validCursorKey(key, sortKind) {
		return CursorKey{}, invalidCursorError()
	}
	return key, nil
}

// EncodeFor creates a Task 4 cursor bound to the endpoint and filters that
// produced it. Cursors must never be reused by another list operation.
func (c *hmacCursorCodec) EncodeFor(audience CursorAudience, key CursorKey, shape CursorShape) (string, error) {
	filter, err := canonicalCursorFilter(audience)
	if err != nil || !validCursorKey(key, shape) {
		return "", invalidCursorError()
	}
	payload, err := json.Marshal(audienceCursorPayload{
		Version:  audienceCursorVersion,
		Endpoint: audience.Endpoint,
		Filter:   filter,
		Shape:    shape,
		Sort:     key.Sort,
		ID:       key.ID,
	})
	if err != nil {
		return "", invalidCursorError()
	}
	signature := c.signAudience(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

// DecodeFor returns a cursor key only when the token is authentic, canonical,
// and was issued for the supplied endpoint, filters, and sort shape.
func (c *hmacCursorCodec) DecodeFor(value string, audience CursorAudience, shape CursorShape) (CursorKey, error) {
	expectedFilter, err := canonicalCursorFilter(audience)
	if err != nil {
		return CursorKey{}, invalidCursorError()
	}
	payload, err := c.verifyAudienceToken(value)
	if err != nil {
		return CursorKey{}, invalidCursorError()
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded audienceCursorPayload
	if err := decoder.Decode(&decoded); err != nil {
		return CursorKey{}, invalidCursorError()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CursorKey{}, invalidCursorError()
	}
	key := CursorKey{Sort: decoded.Sort, ID: decoded.ID}
	canonicalFilter, err := canonicalPayloadFilter(decoded.Filter)
	if err != nil || decoded.Version != audienceCursorVersion || decoded.Shape != shape ||
		decoded.Endpoint != audience.Endpoint || !sameCursorFilter(canonicalFilter, expectedFilter) ||
		!sameCursorFilter(decoded.Filter, canonicalFilter) || !validCursorKey(key, shape) {
		return CursorKey{}, invalidCursorError()
	}
	canonicalPayload, err := json.Marshal(audienceCursorPayload{
		Version: decoded.Version, Endpoint: decoded.Endpoint, Filter: canonicalFilter,
		Shape: decoded.Shape, Sort: decoded.Sort, ID: decoded.ID,
	})
	if err != nil || !bytes.Equal(payload, canonicalPayload) {
		return CursorKey{}, invalidCursorError()
	}
	return key, nil
}

func (c *hmacCursorCodec) sign(sortKind CursorSortKind, payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(sortKind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func (c *hmacCursorCodec) signAudience(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte("xisnove:cursor:v2"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func (c *hmacCursorCodec) verifyAudienceToken(value string) ([]byte, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, errors.New("invalid cursor token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size || !hmac.Equal(signature, c.signAudience(payload)) {
		return nil, errors.New("invalid cursor signature")
	}
	return payload, nil
}

func validCursorKey(key CursorKey, sortKind CursorSortKind) bool {
	if !validUUID(key.ID) {
		return false
	}
	switch sortKind {
	case CursorShapeString:
		return key.Sort != "" && utf8.ValidString(key.Sort)
	case CursorShapeInt:
		parsed, err := strconv.ParseInt(key.Sort, 10, 64)
		return err == nil && key.Sort == strconv.FormatInt(parsed, 10)
	case CursorShapeTime:
		parsed, err := time.Parse(time.RFC3339Nano, key.Sort)
		return err == nil && key.Sort == parsed.UTC().Format(time.RFC3339Nano)
	default:
		return false
	}
}

func canonicalCursorFilter(audience CursorAudience) ([]cursorFilter, error) {
	if !validCursorEndpoint(audience.Endpoint) {
		return nil, errors.New("invalid cursor audience")
	}
	filter := make([]cursorFilter, 0, len(audience.Filter))
	for name, values := range audience.Filter {
		filter = append(filter, cursorFilter{Name: name, Values: append([]string(nil), values...)})
	}
	return canonicalPayloadFilter(filter)
}

func canonicalPayloadFilter(filter []cursorFilter) ([]cursorFilter, error) {
	canonical := append([]cursorFilter(nil), filter...)
	for index := range canonical {
		item := &canonical[index]
		if item.Name == "" || !utf8.ValidString(item.Name) || len(item.Values) == 0 {
			return nil, errors.New("invalid cursor filter")
		}
		item.Values = append([]string(nil), item.Values...)
		sort.Strings(item.Values)
		for valueIndex, value := range item.Values {
			if value == "" || !utf8.ValidString(value) || (valueIndex > 0 && value == item.Values[valueIndex-1]) {
				return nil, errors.New("invalid cursor filter")
			}
		}
	}
	sort.Slice(canonical, func(left, right int) bool { return canonical[left].Name < canonical[right].Name })
	for index := 1; index < len(canonical); index++ {
		if canonical[index].Name == canonical[index-1].Name {
			return nil, errors.New("duplicate cursor filter")
		}
	}
	return canonical, nil
}

func sameCursorFilter(left, right []cursorFilter) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name || len(left[index].Values) != len(right[index].Values) {
			return false
		}
		for valueIndex := range left[index].Values {
			if left[index].Values[valueIndex] != right[index].Values[valueIndex] {
				return false
			}
		}
	}
	return true
}

func validCursorEndpoint(value string) bool {
	return strings.HasPrefix(value, "/") && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "?#") && utf8.ValidString(value) && path.Clean(value) == value
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value != strings.ToLower(value) {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	_, err := hex.Decode(decoded, []byte(compact))
	return err == nil
}

func invalidCursorError() error {
	return &ValidationError{Fields: map[string]string{"cursor": "is invalid"}}
}
