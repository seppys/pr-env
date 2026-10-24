/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package githubwebhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// signatureHeader carries GitHub's HMAC-SHA256 of the raw request body, keyed with the
	// webhook secret.
	signatureHeader = "X-Hub-Signature-256"
	signaturePrefix = "sha256="
)

var errInvalidSignature = errors.New("invalid webhook signature")

// verifySignature checks that header is GitHub's signature of body with secret, proving the
// request came from GitHub and wasn't modified on the way.
func verifySignature(secret, body []byte, header string) error {
	// With an empty key anyone can compute a valid signature, so refuse rather than accept everything.
	if len(secret) == 0 {
		return fmt.Errorf("%w: no webhook secret configured", errInvalidSignature)
	}
	if header == "" {
		return fmt.Errorf("%w: missing %s header", errInvalidSignature, signatureHeader)
	}
	hexSignature, ok := strings.CutPrefix(header, signaturePrefix)
	if !ok {
		return fmt.Errorf("%w: missing %q prefix", errInvalidSignature, signaturePrefix)
	}
	got, err := hex.DecodeString(hexSignature)
	if err != nil {
		return fmt.Errorf("%w: not hex encoded", errInvalidSignature)
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	// hmac.Equal takes the same time wherever the first difference is, so response times don't
	// reveal how much of a forged signature was right.
	if !hmac.Equal(got, mac.Sum(nil)) {
		return fmt.Errorf("%w: signature does not match", errInvalidSignature)
	}
	return nil
}
