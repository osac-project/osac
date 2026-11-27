/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// SignHeaders builds Standard Webhooks HMAC headers for a POST body.
func SignHeaders(msgID, secret string, ts time.Time, body []byte) (map[string]string, error) {
	if msgID == "" {
		return nil, fmt.Errorf("webhook message id is required")
	}
	key, err := parseSigningSecret(secret)
	if err != nil {
		return nil, err
	}
	timestamp := fmt.Sprintf("%d", ts.Unix())
	toSign := msgID + "." + timestamp + "." + string(body)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(toSign))
	sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return map[string]string{
		"webhook-id":        msgID,
		"webhook-timestamp": timestamp,
		"webhook-signature": sig,
	}, nil
}

func parseSigningSecret(secret string) ([]byte, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, fmt.Errorf("signing secret is required")
	}
	const prefix = "whsec_"
	if strings.HasPrefix(secret, prefix) {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, prefix))
		if err != nil {
			// Fall back to std encoding used by some generators.
			raw, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, prefix))
			if err != nil {
				return nil, fmt.Errorf("invalid signing secret encoding: %w", err)
			}
		}
		return raw, nil
	}
	return []byte(secret), nil
}
