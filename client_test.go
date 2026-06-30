// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import (
	"errors"
	"strings"
	"testing"
	"time"

	nethttp "github.com/go-ruby-net-http/net-http"
)

// TestAllOpsTransportError drives every operation through a transport that
// fails, covering each op's roundTrip-error return branch.
func TestAllOpsTransportError(t *testing.T) {
	mk := func() *Client { return newTestClient(&fakeTransport{err: errors.New("net down")}) }
	checks := []func() error{
		func() error { _, e := mk().GetObject(GetObjectInput{Bucket: "b", Key: "k"}); return e },
		func() error { _, e := mk().PutObject(PutObjectInput{Bucket: "b", Key: "k"}); return e },
		func() error { return mk().DeleteObject("b", "k") },
		func() error { _, e := mk().HeadObject("b", "k"); return e },
		func() error { _, e := mk().ListObjectsV2(ListObjectsV2Input{Bucket: "b"}); return e },
		func() error { _, e := mk().ListBuckets(); return e },
		func() error { return mk().CreateBucket("b") },
		func() error { return mk().DeleteBucket("b") },
	}
	for i, check := range checks {
		if err := check(); err == nil {
			t.Errorf("op %d: expected transport error", i)
		}
	}
}

// TestCanonicalQueryDuplicateKeys covers the secondary (by-value) sort branch.
func TestCanonicalQueryDuplicateKeys(t *testing.T) {
	got := canonicalQuery([][2]string{{"k", "b"}, {"k", "a"}})
	if got != "k=a&k=b" {
		t.Errorf("canonicalQuery dup keys = %q", got)
	}
}

// fakeTransport records the request bytes and dial host it was handed, and
// returns canned response bytes (or a canned error). It is the host seam stub:
// it stands in for the TCP/TLS connect+send+recv without touching the network.
type fakeTransport struct {
	gotHost string
	gotReq  []byte
	resp    []byte
	err     error
}

func (f *fakeTransport) RoundTrip(host string, req []byte) ([]byte, error) {
	f.gotHost = host
	f.gotReq = req
	return f.resp, f.err
}

// fixedClock pins the signing instant so request bytes are deterministic.
func fixedClock() time.Time {
	return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
}

func newTestClient(ft *fakeTransport) *Client {
	c := NewClient(awsRegion, NewCredentials(awsAccessKeyID, awsSecretKey), ft)
	c.Now = fixedClock
	return c
}

func httpResp(status int, headers, body string) []byte {
	statusText := map[int]string{200: "OK", 204: "No Content", 404: "Not Found"}[status]
	var b strings.Builder
	b.WriteString("HTTP/1.1 ")
	b.WriteString(itoa(status))
	b.WriteString(" ")
	b.WriteString(statusText)
	b.WriteString("\r\n")
	b.WriteString(headers)
	b.WriteString("Content-Length: ")
	b.WriteString(itoa(len(body)))
	b.WriteString("\r\n\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestGetObject(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "Content-Type: text/plain\r\nETag: \"e1\"\r\n", "hello world")}
	c := newTestClient(ft)
	out, err := c.GetObject(GetObjectInput{Bucket: "examplebucket", Key: "test.txt", Range: "bytes=0-9"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Body) != "hello world" || out.ETag != `"e1"` || out.ContentType != "text/plain" {
		t.Errorf("output = %+v", out)
	}
	if out.ContentLength != int64(len("hello world")) {
		t.Errorf("content-length = %d", out.ContentLength)
	}
	// Verify the request bytes carry the expected request line, host and the
	// signed Authorization with the AWS GET-object signature.
	req := string(ft.gotReq)
	if !strings.HasPrefix(req, "GET /test.txt HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", req)
	}
	if ft.gotHost != "examplebucket.s3.us-east-1.amazonaws.com" {
		t.Errorf("host = %q", ft.gotHost)
	}
	if !strings.Contains(req, "Range: bytes=0-9\r\n") {
		t.Errorf("missing Range header:\n%s", req)
	}
	if !strings.Contains(req, "X-Amz-Content-Sha256: "+EmptyPayloadHash+"\r\n") {
		t.Errorf("missing content-sha256:\n%s", req)
	}
	if !strings.Contains(req, "Authorization: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request") {
		t.Errorf("authorization missing/wrong:\n%s", req)
	}
}

func TestGetObjectNoRange(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", "x")}
	c := newTestClient(ft)
	if _, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ft.gotReq), "Range:") {
		t.Error("should not send Range header when empty")
	}
}

func TestGetObjectS3Error(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	_, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "missing"})
	var e *Error
	if !errors.As(err, &e) || e.Code != "NoSuchKey" {
		t.Errorf("expected NoSuchKey, got %v", err)
	}
}

func TestPutObject(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "ETag: \"put1\"\r\n", "")}
	c := newTestClient(ft)
	out, err := c.PutObject(PutObjectInput{
		Bucket:       "examplebucket",
		Key:          "test$file.text",
		Body:         []byte("Welcome to Amazon S3."),
		ContentType:  "text/plain",
		StorageClass: "REDUCED_REDUNDANCY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ETag != `"put1"` {
		t.Errorf("etag = %q", out.ETag)
	}
	req := string(ft.gotReq)
	if !strings.HasPrefix(req, "PUT /test%24file.text HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", req)
	}
	if !strings.Contains(req, "X-Amz-Storage-Class: REDUCED_REDUNDANCY\r\n") {
		t.Errorf("missing storage class:\n%s", req)
	}
	if !strings.Contains(req, "X-Amz-Content-Sha256: "+putObjectBodyHash+"\r\n") {
		t.Errorf("payload hash wrong:\n%s", req)
	}
	if !strings.HasSuffix(req, "\r\n\r\nWelcome to Amazon S3.") {
		t.Errorf("body not appended:\n%s", req)
	}
}

func TestPutObjectNilBody(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "ETag: \"empty\"\r\n", "")}
	c := newTestClient(ft)
	out, err := c.PutObject(PutObjectInput{Bucket: "b", Key: "k"})
	if err != nil || out.ETag != `"empty"` {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if !strings.Contains(string(ft.gotReq), "X-Amz-Content-Sha256: "+EmptyPayloadHash) {
		t.Error("nil body should hash as empty")
	}
}

func TestPutObjectError(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if _, err := c.PutObject(PutObjectInput{Bucket: "b", Key: "k", Body: []byte("x")}); err == nil {
		t.Error("expected error")
	}
	// Transport-level error path.
	ft2 := &fakeTransport{err: errors.New("boom")}
	c2 := newTestClient(ft2)
	if _, err := c2.PutObject(PutObjectInput{Bucket: "b", Key: "k", Body: []byte("x")}); err == nil {
		t.Error("expected transport error")
	}
}

func TestDeleteObject(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(204, "", "")}
	c := newTestClient(ft)
	if err := c.DeleteObject("examplebucket", "old.txt"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(ft.gotReq), "DELETE /old.txt HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", ft.gotReq)
	}
}

func TestDeleteObjectError(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if err := c.DeleteObject("b", "k"); err == nil {
		t.Error("expected error")
	}
}

func TestHeadObject(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200,
		"ETag: \"h1\"\r\nContent-Type: image/jpeg\r\nLast-Modified: Wed, 12 Oct 2009 17:50:00 GMT\r\n", "")}
	c := newTestClient(ft)
	out, err := c.HeadObject("examplebucket", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if out.ETag != `"h1"` || out.ContentType != "image/jpeg" ||
		out.LastModified != "Wed, 12 Oct 2009 17:50:00 GMT" {
		t.Errorf("head out = %+v", out)
	}
	if !strings.HasPrefix(string(ft.gotReq), "HEAD /photo.jpg HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", ft.gotReq)
	}
}

func TestHeadObjectError(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if _, err := c.HeadObject("b", "k"); err == nil {
		t.Error("expected error")
	}
}

func TestListObjectsV2(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "Content-Type: application/xml\r\n", listObjectsV2XML)}
	c := newTestClient(ft)
	out, err := c.ListObjectsV2(ListObjectsV2Input{
		Bucket:            "example-bucket",
		Prefix:            "photos/",
		Delimiter:         "/",
		MaxKeys:           2,
		ContinuationToken: "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Contents) != 2 || !out.IsTruncated {
		t.Errorf("listing wrong: %+v", out)
	}
	req := string(ft.gotReq)
	if !strings.HasPrefix(req, "GET /?") {
		t.Errorf("request line wrong:\n%s", req)
	}
	for _, want := range []string{"list-type=2", "prefix=photos%2F", "delimiter=%2F", "max-keys=2", "continuation-token=tok"} {
		if !strings.Contains(req, want) {
			t.Errorf("query missing %q:\n%s", want, req)
		}
	}
}

func TestListObjectsV2Minimal(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", `<ListBucketResult><Name>b</Name></ListBucketResult>`)}
	c := newTestClient(ft)
	out, err := c.ListObjectsV2(ListObjectsV2Input{Bucket: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "b" {
		t.Errorf("name = %q", out.Name)
	}
	// Only list-type=2 in the query.
	req := string(ft.gotReq)
	if !strings.Contains(req, "GET /?list-type=2 HTTP/1.1") {
		t.Errorf("query wrong:\n%s", req)
	}
}

func TestListObjectsV2Error(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if _, err := c.ListObjectsV2(ListObjectsV2Input{Bucket: "b"}); err == nil {
		t.Error("expected error")
	}
	// Malformed body on a 200 -> parse error.
	ft2 := &fakeTransport{resp: httpResp(200, "", "<<<")}
	c2 := newTestClient(ft2)
	if _, err := c2.ListObjectsV2(ListObjectsV2Input{Bucket: "b"}); err == nil {
		t.Error("expected parse error")
	}
}

func TestListBuckets(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", listBucketsXML)}
	c := newTestClient(ft)
	out, err := c.ListBuckets()
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Buckets) != 2 {
		t.Errorf("buckets = %d", len(out.Buckets))
	}
	if ft.gotHost != "s3.us-east-1.amazonaws.com" {
		t.Errorf("service host = %q", ft.gotHost)
	}
	if !strings.HasPrefix(string(ft.gotReq), "GET / HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", ft.gotReq)
	}
}

func TestListBucketsError(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if _, err := c.ListBuckets(); err == nil {
		t.Error("expected error")
	}
	ft2 := &fakeTransport{resp: httpResp(200, "", "<<<")}
	c2 := newTestClient(ft2)
	if _, err := c2.ListBuckets(); err == nil {
		t.Error("expected parse error")
	}
}

func TestCreateBucketUSEast1(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", "")}
	c := newTestClient(ft) // region us-east-1: no body
	if err := c.CreateBucket("newbucket"); err != nil {
		t.Fatal(err)
	}
	req := string(ft.gotReq)
	if !strings.HasPrefix(req, "PUT / HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", req)
	}
	if strings.Contains(req, "CreateBucketConfiguration") {
		t.Error("us-east-1 should not send a LocationConstraint body")
	}
}

func TestCreateBucketOtherRegion(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", "")}
	c := NewClient("eu-west-1", NewCredentials(awsAccessKeyID, awsSecretKey), ft)
	c.Now = fixedClock
	if err := c.CreateBucket("newbucket"); err != nil {
		t.Fatal(err)
	}
	req := string(ft.gotReq)
	if !strings.Contains(req, "<LocationConstraint>eu-west-1</LocationConstraint>") {
		t.Errorf("missing LocationConstraint:\n%s", req)
	}
	if !strings.HasSuffix(req, "</CreateBucketConfiguration>") {
		t.Errorf("body not appended:\n%s", req)
	}
}

func TestCreateBucketError(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if err := c.CreateBucket("b"); err == nil {
		t.Error("expected error")
	}
}

func TestDeleteBucket(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(204, "", "")}
	c := newTestClient(ft)
	if err := c.DeleteBucket("oldbucket"); err != nil {
		t.Fatal(err)
	}
	if ft.gotHost != "oldbucket.s3.us-east-1.amazonaws.com" {
		t.Errorf("host = %q", ft.gotHost)
	}
	if !strings.HasPrefix(string(ft.gotReq), "DELETE / HTTP/1.1\r\n") {
		t.Errorf("request line wrong:\n%s", ft.gotReq)
	}
}

func TestDeleteBucketError(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(404, "", s3ErrorXML)}
	c := newTestClient(ft)
	if err := c.DeleteBucket("b"); err == nil {
		t.Error("expected error")
	}
}

func TestSessionToken(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", "x")}
	c := NewClient(awsRegion,
		NewCredentials(awsAccessKeyID, awsSecretKey).WithSessionToken("SESSIONTOKEN123"), ft)
	c.Now = fixedClock
	if _, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	req := string(ft.gotReq)
	if !strings.Contains(req, "X-Amz-Security-Token: SESSIONTOKEN123\r\n") {
		t.Errorf("missing security token:\n%s", req)
	}
	// The token must also be in the signed headers.
	if !strings.Contains(req, "SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-security-token") {
		t.Errorf("token not in signed headers:\n%s", req)
	}
}

func TestPathStyleAndEndpoint(t *testing.T) {
	ft := &fakeTransport{resp: httpResp(200, "", "x")}
	c := NewClient(awsRegion, NewCredentials(awsAccessKeyID, awsSecretKey), ft)
	c.Now = fixedClock
	c.UsePathStyle = true
	if _, err := c.GetObject(GetObjectInput{Bucket: "mybucket", Key: "a/b.txt"}); err != nil {
		t.Fatal(err)
	}
	if ft.gotHost != "s3.us-east-1.amazonaws.com" {
		t.Errorf("path-style host = %q", ft.gotHost)
	}
	if !strings.HasPrefix(string(ft.gotReq), "GET /mybucket/a/b.txt HTTP/1.1\r\n") {
		t.Errorf("path-style request line wrong:\n%s", ft.gotReq)
	}

	// Custom endpoint, key absent -> "/<bucket>".
	ft2 := &fakeTransport{resp: httpResp(200, "", "")}
	c2 := NewClient(awsRegion, NewCredentials(awsAccessKeyID, awsSecretKey), ft2)
	c2.Now = fixedClock
	c2.Endpoint = "localhost:9000"
	if err := c2.CreateBucket("mybucket"); err != nil {
		t.Fatal(err)
	}
	if c2.host("mybucket") != "localhost:9000" {
		t.Errorf("endpoint host = %q", c2.host("mybucket"))
	}
	if !strings.HasPrefix(string(ft2.gotReq), "PUT /mybucket HTTP/1.1\r\n") {
		t.Errorf("endpoint request line wrong:\n%s", ft2.gotReq)
	}
}

func TestBuildNewRequestError(t *testing.T) {
	// An unknown HTTP method makes net-http's NewRequest fail; build must
	// propagate that error.
	c := newTestClient(&fakeTransport{})
	if _, _, err := c.build(op{method: "BOGUS", bucket: "b", key: "k"}); err == nil {
		t.Error("expected NewRequest error for unknown method")
	}
}

func TestBuildPropagatesViaRoundTrip(t *testing.T) {
	// roundTrip surfaces a build error (here via an unknown method) before the
	// transport is consulted.
	c := newTestClient(&fakeTransport{})
	if _, err := c.roundTrip(op{method: "BOGUS", bucket: "b", key: "k"}); err == nil {
		t.Error("expected build error from roundTrip")
	}
}

func TestBuildSerializeError(t *testing.T) {
	// Force the (normally unreachable) serialisation-error branch via the seam.
	orig := serializeRequest
	serializeRequest = func(*nethttp.Request) ([]byte, error) {
		return nil, errors.New("serialize boom")
	}
	defer func() { serializeRequest = orig }()
	c := newTestClient(&fakeTransport{})
	if _, _, err := c.build(op{method: "GET", bucket: "b", key: "k"}); err == nil ||
		!strings.Contains(err.Error(), "serialize boom") {
		t.Errorf("expected serialize error, got %v", err)
	}
}

func TestParseInt64Negative(t *testing.T) {
	if parseInt64("-5") != -5 {
		t.Error("parseInt64 negative")
	}
	if parseInt64("nan") != 0 {
		t.Error("parseInt64 bad -> 0")
	}
}

func TestNoTransport(t *testing.T) {
	c := NewClient(awsRegion, NewCredentials(awsAccessKeyID, awsSecretKey), nil)
	c.Now = fixedClock
	if _, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "k"}); err == nil ||
		!strings.Contains(err.Error(), "no Transport") {
		t.Errorf("expected no-transport error, got %v", err)
	}
}

func TestTransportError(t *testing.T) {
	ft := &fakeTransport{err: errors.New("dial failed")}
	c := newTestClient(ft)
	if _, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "k"}); err == nil ||
		!strings.Contains(err.Error(), "dial failed") {
		t.Errorf("expected transport error, got %v", err)
	}
}

func TestMalformedResponseFromTransport(t *testing.T) {
	ft := &fakeTransport{resp: []byte("garbage-no-crlf")}
	c := newTestClient(ft)
	if _, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "k"}); err == nil {
		t.Error("expected parse error on malformed response")
	}
}

func TestDefaultClockUsed(t *testing.T) {
	// Exercise the time.Now branch (c.Now == nil) without asserting the stamp.
	ft := &fakeTransport{resp: httpResp(200, "", "x")}
	c := NewClient(awsRegion, NewCredentials(awsAccessKeyID, awsSecretKey), ft)
	if _, err := c.GetObject(GetObjectInput{Bucket: "b", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ft.gotReq), "X-Amz-Date:") {
		t.Error("x-amz-date should be present")
	}
}
