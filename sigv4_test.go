// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import (
	"encoding/hex"
	"testing"
)

// The AWS-published S3 SigV4 examples. The credentials, region, service and date
// are the ones AWS uses in the S3 "Signature Calculations for the Authorization
// Header" worked examples; the expected canonical-request hash, string-to-sign,
// signing key and signature are byte-for-byte from those examples (independently
// reproduced — see the repo's commit message / report).
const (
	awsAccessKeyID = "AKIAIOSFODNN7EXAMPLE"
	awsSecretKey   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	awsRegion      = "us-east-1"
	awsDateStamp   = "20130524"
	awsAmzDate     = "20130524T000000Z"
)

// TestSigningKeyDerivation checks the kDate->kRegion->kService->kSigning chain
// against the value derived for the AWS GET-object example.
func TestSigningKeyDerivation(t *testing.T) {
	key := SigningKey(awsSecretKey, awsDateStamp, awsRegion, "s3")
	got := hex.EncodeToString(key)
	want := "dbb893acc010964918f1fd433add87c70e8b0db6be30c1fbeafefa5ec6ba8378"
	if got != want {
		t.Fatalf("SigningKey = %s, want %s", got, want)
	}
}

// TestSigV4GetObjectVector validates the full SigV4 chain for the AWS GET Object
// example (a Range request on examplebucket.s3.amazonaws.com), asserting the
// canonical request, the string-to-sign, the signature, and the Authorization
// header byte-for-byte.
func TestSigV4GetObjectVector(t *testing.T) {
	signer := Signer{
		Credentials: NewCredentials(awsAccessKeyID, awsSecretKey),
		Region:      awsRegion,
	}
	cr := CanonicalRequest{
		Method:       "GET",
		CanonicalURI: "/test.txt",
		Headers: [][2]string{
			{"host", "examplebucket.s3.amazonaws.com"},
			{"range", "bytes=0-9"},
			{"x-amz-content-sha256", EmptyPayloadHash},
			{"x-amz-date", awsAmzDate},
		},
		PayloadHash: EmptyPayloadHash,
	}
	res := signer.Sign(cr, awsDateStamp, awsAmzDate)

	wantCanonical := "GET\n" +
		"/test.txt\n" +
		"\n" +
		"host:examplebucket.s3.amazonaws.com\n" +
		"range:bytes=0-9\n" +
		"x-amz-content-sha256:" + EmptyPayloadHash + "\n" +
		"x-amz-date:20130524T000000Z\n" +
		"\n" +
		"host;range;x-amz-content-sha256;x-amz-date\n" +
		EmptyPayloadHash
	if res.CanonicalRequest != wantCanonical {
		t.Errorf("canonical request mismatch:\n got %q\nwant %q", res.CanonicalRequest, wantCanonical)
	}

	wantSTS := "AWS4-HMAC-SHA256\n" +
		"20130524T000000Z\n" +
		"20130524/us-east-1/s3/aws4_request\n" +
		"7344ae5b7ee6c3e7e6b0fe0640412a37625d1fbfff95c48bbb2dc43964946972"
	if res.StringToSign != wantSTS {
		t.Errorf("string-to-sign mismatch:\n got %q\nwant %q", res.StringToSign, wantSTS)
	}

	wantSig := "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if res.Signature != wantSig {
		t.Errorf("signature = %s, want %s", res.Signature, wantSig)
	}

	wantAuth := "AWS4-HMAC-SHA256 " +
		"Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date," +
		"Signature=" + wantSig
	if res.Authorization != wantAuth {
		t.Errorf("authorization mismatch:\n got %q\nwant %q", res.Authorization, wantAuth)
	}
	if res.SignedHeaders != "host;range;x-amz-content-sha256;x-amz-date" {
		t.Errorf("signed headers = %q", res.SignedHeaders)
	}
}

// putObjectBodyHash is the SHA-256 of the AWS PUT Object example body
// ("Welcome to Amazon S3."), matching the x-amz-content-sha256 in that example.
const putObjectBodyHash = "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072"

// TestSigV4PutObjectVector validates the full SigV4 chain for the AWS PUT Object
// example (an upload to /test$file.text with a storage-class and date header).
func TestSigV4PutObjectVector(t *testing.T) {
	// Confirm our payload hasher reproduces the example body hash.
	if got := HashPayload([]byte("Welcome to Amazon S3.")); got != putObjectBodyHash {
		t.Fatalf("body hash = %s, want %s", got, putObjectBodyHash)
	}

	signer := Signer{
		Credentials: NewCredentials(awsAccessKeyID, awsSecretKey),
		Region:      awsRegion,
	}
	cr := CanonicalRequest{
		Method:       "PUT",
		CanonicalURI: "/test%24file.text",
		Headers: [][2]string{
			{"date", "Fri, 24 May 2013 00:00:00 GMT"},
			{"host", "examplebucket.s3.amazonaws.com"},
			{"x-amz-content-sha256", putObjectBodyHash},
			{"x-amz-date", awsAmzDate},
			{"x-amz-storage-class", "REDUCED_REDUNDANCY"},
		},
		PayloadHash: putObjectBodyHash,
	}
	res := signer.Sign(cr, awsDateStamp, awsAmzDate)

	wantCanonical := "PUT\n" +
		"/test%24file.text\n" +
		"\n" +
		"date:Fri, 24 May 2013 00:00:00 GMT\n" +
		"host:examplebucket.s3.amazonaws.com\n" +
		"x-amz-content-sha256:" + putObjectBodyHash + "\n" +
		"x-amz-date:20130524T000000Z\n" +
		"x-amz-storage-class:REDUCED_REDUNDANCY\n" +
		"\n" +
		"date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class\n" +
		putObjectBodyHash
	if res.CanonicalRequest != wantCanonical {
		t.Errorf("canonical request mismatch:\n got %q\nwant %q", res.CanonicalRequest, wantCanonical)
	}

	wantSig := "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"
	if res.Signature != wantSig {
		t.Errorf("signature = %s, want %s", res.Signature, wantSig)
	}
}

// TestCredentialScope checks the scope-string assembly.
func TestCredentialScope(t *testing.T) {
	if got := CredentialScope("20130524", "us-east-1", "s3"); got != "20130524/us-east-1/s3/aws4_request" {
		t.Errorf("CredentialScope = %q", got)
	}
}

// TestHashPayloadEmpty checks the empty-body hash constant.
func TestHashPayloadEmpty(t *testing.T) {
	if got := HashPayload(nil); got != EmptyPayloadHash {
		t.Errorf("HashPayload(nil) = %s, want %s", got, EmptyPayloadHash)
	}
	if got := HashPayload([]byte{}); got != EmptyPayloadHash {
		t.Errorf("HashPayload(empty) = %s, want %s", got, EmptyPayloadHash)
	}
}

// TestCanonicalURIDefault checks the empty-URI default and multi-value header
// folding plus the trimall whitespace rule.
func TestCanonicalURIAndHeaderFolding(t *testing.T) {
	cr := CanonicalRequest{
		Method: "GET",
		Headers: [][2]string{
			{"host", "x"},
			{"X-Multi", "  a   b  "},
			{"x-multi", "c"},
		},
		PayloadHash: EmptyPayloadHash,
	}
	canonical, signed := cr.String()
	if signed != "host;x-multi" {
		t.Errorf("signed = %q", signed)
	}
	want := "GET\n/\n\nhost:x\nx-multi:a b,c\n\nhost;x-multi\n" + EmptyPayloadHash
	if canonical != want {
		t.Errorf("canonical:\n got %q\nwant %q", canonical, want)
	}
}

// TestCanonicalQuery exercises query encoding, sorting and empty-value keys.
func TestCanonicalQuery(t *testing.T) {
	q := [][2]string{
		{"list-type", "2"},
		{"prefix", "a b/c"},
		{"acl", ""},
	}
	got := canonicalQuery(q)
	want := "acl=&list-type=2&prefix=a%20b%2Fc"
	if got != want {
		t.Errorf("canonicalQuery = %q, want %q", got, want)
	}
	if canonicalQuery(nil) != "" {
		t.Errorf("empty query should be empty string")
	}
}

// TestUnsignedPayloadConst guards the streaming sentinel constant.
func TestUnsignedPayloadConst(t *testing.T) {
	if UnsignedPayload != "UNSIGNED-PAYLOAD" {
		t.Errorf("UnsignedPayload = %q", UnsignedPayload)
	}
}
