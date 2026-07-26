package input

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maxInputBytes = 1 << 20

func DecodeFile(path string, stdin io.Reader, target any) error {
	if target == nil {
		return errors.New("input target is required")
	}
	var reader io.Reader
	if path == "-" {
		if stdin == nil {
			return errors.New("stdin is unavailable")
		}
		reader = stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open request file: %w", err)
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	if len(data) > maxInputBytes {
		return fmt.Errorf("request exceeds %d bytes", maxInputBytes)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("request is empty")
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		var document any
		if err := yaml.Unmarshal(trimmed, &document); err != nil {
			return fmt.Errorf("decode YAML request: %w", err)
		}
		trimmed, err = json.Marshal(document)
		if err != nil {
			return fmt.Errorf("normalize YAML request: %w", err)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode request: multiple documents are not allowed")
		}
		return fmt.Errorf("decode trailing request data: %w", err)
	}
	return nil
}

func ReadSecretLine(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errors.New("secret input is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	if len(data) > 4096 {
		return "", errors.New("secret exceeds 4096 bytes")
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("secret must not be empty")
	}
	return value, nil
}
