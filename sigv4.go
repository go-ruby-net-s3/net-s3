// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package nets3 is a pure-Go (CGO=0) Ruby-style Amazon S3 client core. It builds
// signed S3 REST requests — AWS Signature Version 4 request signing, the S3 REST
// request building, and S3 XML response parsing — while leaving the actual
// HTTPS transport (the TCP/TLS connect+send+recv) to a host-supplied seam.
//
// The signing is validated byte-for-byte against AWS's published SigV4 example
// vectors (see sigv4_test.go); the XML parsing is validated against canned S3
// responses (see response_test.go). No network access is performed by this
// package; it is a deterministic byte producer + response decoder.
package nets3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// EmptyPayloadHash is the SHA-256 of the empty string, the x-amz-content-sha256
// value used for bodyless requests (GET/HEAD/DELETE/List…). It is a constant in
// every AWS SDK.
const EmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// UnsignedPayload is the sentinel x-amz-content-sha256 value that tells S3 the
// payload is not included in the signature (used for streaming bodies).
const UnsignedPayload = "UNSIGNED-PAYLOAD"

// algorithm is the SigV4 algorithm identifier that opens the string-to-sign and
// the Authorization header.
const algorithm = "AWS4-HMAC-SHA256"

// service is the S3 service identifier in the credential scope.
const service = "s3"

// terminator closes the credential scope.
const terminator = "aws4_request"

// hmacSHA256 returns HMAC-SHA256(key, data).
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashPayload returns the lowercase-hex SHA-256 of a request body, the value
// that goes in the x-amz-content-sha256 header. A nil body hashes as the empty
// string, yielding EmptyPayloadHash.
func HashPayload(body []byte) string {
	return sha256Hex(body)
}

// SigningKey derives the SigV4 signing key for the given secret access key, the
// date stamp (yyyymmdd), the AWS region, and the service. It is the HMAC-SHA256
// chain kDate -> kRegion -> kService -> kSigning that AWS specifies:
//
//	kDate    = HMAC("AWS4"+secret, date)
//	kRegion  = HMAC(kDate,         region)
//	kService = HMAC(kRegion,       service)
//	kSigning = HMAC(kService,      "aws4_request")
func SigningKey(secret, date, region, svc string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(svc))
	return hmacSHA256(kService, []byte(terminator))
}

// CredentialScope is the "date/region/service/aws4_request" scope string.
func CredentialScope(date, region, svc string) string {
	return date + "/" + region + "/" + svc + "/" + terminator
}

// CanonicalRequest is the parsed input to SigV4 canonicalisation: the HTTP
// method, the (already URI-encoded) path, the query parameters, the headers to
// sign, and the hex payload hash.
type CanonicalRequest struct {
	Method       string
	CanonicalURI string
	Query        [][2]string // raw (un-encoded) key/value pairs
	Headers      [][2]string // raw header name/value pairs (names case-insensitive)
	PayloadHash  string
}

// canonicalQuery builds the canonical query string: each key and value
// URI-encoded, sorted by encoded key (then by encoded value), joined "k=v" with
// "&". Keys with no value encode as "k=".
func canonicalQuery(query [][2]string) string {
	if len(query) == 0 {
		return ""
	}
	type kv struct{ k, v string }
	pairs := make([]kv, 0, len(query))
	for _, q := range query {
		pairs = append(pairs, kv{uriEncode(q[0], true), uriEncode(q[1], true)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

// canonicalHeaders builds the canonical-headers block and the signed-headers
// list. Header names are lowercased; values have leading/trailing whitespace
// trimmed and internal runs of spaces collapsed. Multiple values for one name
// join with ",". Both outputs are sorted by lowercased header name.
func canonicalHeaders(headers [][2]string) (block, signed string) {
	grouped := map[string][]string{}
	var names []string
	for _, h := range headers {
		name := strings.ToLower(strings.TrimSpace(h[0]))
		if _, ok := grouped[name]; !ok {
			names = append(names, name)
		}
		grouped[name] = append(grouped[name], trimHeaderValue(h[1]))
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(":")
		b.WriteString(strings.Join(grouped[name], ","))
		b.WriteString("\n")
	}
	return b.String(), strings.Join(names, ";")
}

// trimHeaderValue trims surrounding whitespace and collapses internal runs of
// spaces to a single space, per the SigV4 trimall rule.
func trimHeaderValue(v string) string {
	v = strings.TrimSpace(v)
	var b strings.Builder
	space := false
	for _, r := range v {
		if r == ' ' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// String renders the canonical request string SigV4 hashes: method, canonical
// URI, canonical query, canonical headers, signed-headers, payload hash — each
// on its own line. It also returns the signed-headers list separately, since the
// Authorization header needs it.
func (c CanonicalRequest) String() (canonical, signedHeaders string) {
	block, signed := canonicalHeaders(c.Headers)
	uri := c.CanonicalURI
	if uri == "" {
		uri = "/"
	}
	var b strings.Builder
	b.WriteString(c.Method)
	b.WriteString("\n")
	b.WriteString(uri)
	b.WriteString("\n")
	b.WriteString(canonicalQuery(c.Query))
	b.WriteString("\n")
	b.WriteString(block)
	b.WriteString("\n")
	b.WriteString(signed)
	b.WriteString("\n")
	b.WriteString(c.PayloadHash)
	return b.String(), signed
}

// StringToSign builds the SigV4 string-to-sign from the request timestamp
// (yyyymmddThhmmssZ), the credential scope, and the hashed canonical request.
func StringToSign(amzDate, scope, hashedCanonical string) string {
	return algorithm + "\n" + amzDate + "\n" + scope + "\n" + hashedCanonical
}

// Signer holds the immutable signing inputs: the AWS credentials and the region.
type Signer struct {
	Credentials Credentials
	Region      string
}

// SignResult bundles the products of signing a canonical request: the canonical
// request string, the string-to-sign, the signature hex, and the ready-made
// Authorization header value.
type SignResult struct {
	CanonicalRequest string
	StringToSign     string
	Signature        string
	Authorization    string
	SignedHeaders    string
}

// Sign computes the full SigV4 products for a canonical request at the given
// instant. dateStamp is yyyymmdd and amzDate is yyyymmddThhmmssZ (the caller
// supplies both so the result is deterministic and testable). It returns every
// intermediate string so they can each be asserted against the AWS vectors.
func (s Signer) Sign(cr CanonicalRequest, dateStamp, amzDate string) SignResult {
	canonical, signed := cr.String()
	scope := CredentialScope(dateStamp, s.Region, service)
	sts := StringToSign(amzDate, scope, sha256Hex([]byte(canonical)))
	key := SigningKey(s.Credentials.SecretAccessKey, dateStamp, s.Region, service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(sts)))
	auth := algorithm +
		" Credential=" + s.Credentials.AccessKeyID + "/" + scope +
		",SignedHeaders=" + signed +
		",Signature=" + signature
	return SignResult{
		CanonicalRequest: canonical,
		StringToSign:     sts,
		Signature:        signature,
		Authorization:    auth,
		SignedHeaders:    signed,
	}
}
