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
	"strings"
	"time"
	"unicode/utf8"
)

const cursorVersion = 1

type CursorSortKind string

const (
	CursorSortString CursorSortKind = "string"
	CursorSortTime   CursorSortKind = "time"
)

type CursorKey struct {
	Sort string
	ID   string
}

type CursorCodec interface {
	Encode(CursorKey, CursorSortKind) (string, error)
	Decode(string, CursorSortKind) (CursorKey, error)
}

type hmacCursorCodec struct {
	key []byte
}

type cursorPayload struct {
	Version int    `json:"v"`
	Sort    string `json:"sort"`
	ID      string `json:"id"`
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

func (c *hmacCursorCodec) sign(sortKind CursorSortKind, payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(sortKind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func validCursorKey(key CursorKey, sortKind CursorSortKind) bool {
	if !validUUID(key.ID) {
		return false
	}
	switch sortKind {
	case CursorSortString:
		return key.Sort != "" && utf8.ValidString(key.Sort)
	case CursorSortTime:
		parsed, err := time.Parse(time.RFC3339Nano, key.Sort)
		return err == nil && key.Sort == parsed.UTC().Format(time.RFC3339Nano)
	default:
		return false
	}
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
