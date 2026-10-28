// SPDX-License-Identifier: Apache-2.0

// Package spike contains development-only Codex contract and recording tools.
package spike

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/arvel-dev/arvel-sdk/redact"
)

var emailPattern = regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
var personalPath = regexp.MustCompile(`(?i)(?:/Users/|/home/|[A-Z]:[\\/]Users[\\/])[^\s"'<>]+`)
var uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var sanitizedID = regexp.MustCompile(`^id_[0-9a-f]{16}$`)
var embeddedURL = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// Sanitize removes credentials and personal identifiers before persistence.
// Invalid JSON is rejected without including the rejected input in the error.
func Sanitize(message json.RawMessage) (json.RawMessage, error) {
	return sanitizeWithRedactor(message, redact.New())
}

func sanitizeWithRedactor(message json.RawMessage, r *redact.Redactor) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !json.Valid(message) {
		return nil, errors.New("sanitize: invalid JSON")
	}
	registerSecrets(value, "", r)
	var scrub func(any, string) any
	scrub = func(value any, key string) any {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			if sensitiveKey(strings.ToLower(key)) {
				for k := range v {
					out[k] = "[REDACTED]"
				}
				return out
			}
			if name, ok := v["name"].(string); ok && sensitiveKey(strings.ToLower(name)) {
				for k, child := range v {
					if k == "value" {
						out[k] = "[REDACTED]"
					} else {
						out[k] = scrub(child, k)
					}
				}
				return out
			}
			for k, child := range v {
				if strings.EqualFold(key, "env") {
					out[k] = "[REDACTED]"
					continue
				}
				out[k] = scrub(child, k)
			}
			return out
		case []any:
			for i := range v {
				v[i] = scrub(v[i], key)
			}
			return v
		case string:
			lower := strings.ToLower(key)
			if sensitiveKey(lower) {
				return "[REDACTED]"
			}
			if strings.EqualFold(key, "email") {
				return "user@example.invalid"
			}
			if lower == "cwd" || lower == "codexhome" || lower == "path" || lower == "localpath" || lower == "root" {
				if strings.HasPrefix(v, "/") || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(v) {
					return "/scratch"
				}
			}
			if key == "id" || strings.HasSuffix(key, "Id") {
				if sanitizedID.MatchString(v) {
					return v
				}
				return fmt.Sprintf("id_%x", sha256.Sum256([]byte(v)))[:19]
			}
			if u, err := url.Parse(v); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
				return "https://example.invalid/redacted"
			}
			v = emailPattern.ReplaceAllString(v, "user@example.invalid")
			v = embeddedURL.ReplaceAllString(v, "https://example.invalid/redacted")
			v = personalPath.ReplaceAllString(v, "/scratch")
			v = uuidPattern.ReplaceAllStringFunc(v, func(id string) string { return fmt.Sprintf("id_%x", sha256.Sum256([]byte(id)))[:19] })
			return r.String(v)
		default:
			return v
		}
	}
	out, err := json.Marshal(scrub(value, ""))
	if err != nil {
		return nil, errors.New("sanitize: encode scrubbed JSON")
	}
	return out, nil
}

func sensitiveKey(key string) bool {
	key = strings.NewReplacer("_", "", "-", "").Replace(key)
	for _, needle := range []string{"apikey", "password", "authorization", "credential", "secret", "accesstoken", "refreshtoken", "idtoken", "usercode", "authurl", "verificationurl", "cookie"} {
		if strings.Contains(key, needle) {
			return true
		}
	}
	return strings.HasSuffix(key, "token")
}

// registerSecrets remembers credential values before any field is masked, so a
// later vendor error cannot expose a known challenge or token as ordinary text.
func registerSecrets(value any, key string, r *redact.Redactor) {
	switch v := value.(type) {
	case map[string]any:
		name, _ := v["name"].(string)
		force := sensitiveKey(strings.ToLower(key)) || (strings.EqualFold(key, "env") && name == "")
		for k, child := range v {
			childKey := k
			if force || (k == "value" && sensitiveKey(strings.ToLower(name))) {
				childKey = "secret"
			}
			registerSecrets(child, childKey, r)
		}
	case []any:
		for _, child := range v {
			registerSecrets(child, key, r)
		}
	case string:
		if sensitiveKey(strings.ToLower(key)) && v != "[REDACTED]" && v != redact.Replacement {
			r.Add(v)
			r.Add(url.QueryEscape(v))
			r.Add(url.PathEscape(v))
		}
	}
}
