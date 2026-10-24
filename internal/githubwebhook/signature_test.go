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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// testSecret is the webhook secret the tests sign deliveries with.
var testSecret = []byte("test-webhook-secret")

// sign returns the X-Hub-Signature-256 value GitHub would send for body.
func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

var _ = Describe("verifySignature", func() {
	body := []byte(`{"action":"opened"}`)

	It("accepts GitHub's signature of the body", func() {
		Expect(verifySignature(testSecret, body, sign(testSecret, body))).To(Succeed())
	})

	It("matches GitHub's documented example", func() {
		// From GitHub's "Validating webhook deliveries" documentation.
		Expect(verifySignature([]byte("It's a Secret to Everybody"), []byte("Hello, World!"),
			"sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17")).To(Succeed())
	})

	DescribeTable("rejects",
		func(secret []byte, header string) {
			Expect(verifySignature(secret, body, header)).To(MatchError(errInvalidSignature))
		},
		Entry("a signature made with another secret", testSecret, sign([]byte("other-secret"), body)),
		Entry("a signature of another body", testSecret, sign(testSecret, []byte(`{"action":"closed"}`))),
		Entry("a missing header", testSecret, ""),
		Entry("a header without the sha256= prefix", testSecret, sign(testSecret, body)[len(signaturePrefix):]),
		Entry("a SHA-1 signature header", testSecret, "sha1=0123456789abcdef0123456789abcdef01234567"),
		Entry("a header that isn't hex", testSecret, "sha256=not-hex"),
		Entry("a truncated signature", testSecret, sign(testSecret, body)[:20]),
		Entry("any signature when no secret is configured", []byte{}, sign([]byte{}, body)),
	)
})
