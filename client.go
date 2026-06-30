// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import (
	"errors"
	"strconv"
	"strings"
	"time"

	nethttp "github.com/go-ruby-net-http/net-http"
)

// Transport is the host seam: it sends the signed HTTP/1.1 request bytes to S3
// over the (TLS) socket and returns the raw response bytes. This package never
// touches the network itself — rbgo/the host supplies a Transport that does the
// TCP/TLS connect+send+recv. The host field tells the transport where to dial
// (e.g. "examplebucket.s3.us-east-1.amazonaws.com:443").
type Transport interface {
	RoundTrip(host string, requestBytes []byte) (responseBytes []byte, err error)
}

// Clock returns the current time; the client uses it to stamp x-amz-date. It is
// injectable so signing is deterministic in tests. The returned time is used in
// UTC.
type Clock func() time.Time

// Client is a Ruby-style S3 client. It is constructed with a region and
// credentials and a Transport seam, then exposes the S3 operations. The HTTPS
// transport (TCP/TLS connect+send+recv) is the host's responsibility via
// Transport; everything Client itself does is pure request building, signing,
// and response parsing.
type Client struct {
	Region      string
	Credentials Credentials
	Transport   Transport

	// Endpoint, when set, overrides the derived S3 host (e.g. for a custom or
	// path-style endpoint like a MinIO server "localhost:9000"). When empty,
	// the virtual-hosted-style host "<bucket>.s3.<region>.amazonaws.com" is used.
	Endpoint string

	// UsePathStyle selects path-style addressing ("s3.<region>.amazonaws.com/<bucket>/<key>")
	// instead of the default virtual-hosted style. Path-style is needed for
	// non-AWS endpoints and bucket names that are not DNS-compatible.
	UsePathStyle bool

	// Now returns the signing instant. When nil, time.Now is used. Injected in
	// tests for deterministic x-amz-date stamps.
	Now Clock
}

// NewClient builds a client for region with credentials, sending requests
// through transport.
func NewClient(region string, creds Credentials, transport Transport) *Client {
	return &Client{Region: region, Credentials: creds, Transport: transport}
}

// now returns the current signing instant in UTC.
func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// host returns the HTTP Host header value (and dial target authority) for a
// bucket. Virtual-hosted style by default; path-style or a custom Endpoint
// override it. An empty bucket (the ListBuckets / service endpoint) targets the
// regional service host.
func (c *Client) host(bucket string) string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	regional := "s3." + c.Region + ".amazonaws.com"
	if bucket == "" || c.UsePathStyle {
		return regional
	}
	return bucket + "." + regional
}

// path joins the bucket and key into the request path for the addressing style
// in effect. Virtual-hosted style puts only the key in the path; path-style
// prefixes "/<bucket>".
func (c *Client) path(bucket, key string) string {
	keyPath := EncodeKeyPath(key)
	if bucket != "" && (c.UsePathStyle || c.Endpoint != "") {
		if keyPath == "/" {
			return "/" + bucket
		}
		return "/" + bucket + keyPath
	}
	return keyPath
}

// op is a fully-described S3 operation ready to be signed and (optionally) sent.
type op struct {
	method  string
	bucket  string
	key     string
	query   [][2]string
	headers [][2]string // extra user headers (e.g. Content-Type, x-amz-storage-class)
	body    []byte
}

// build turns an op into the signed HTTP/1.1 request bytes plus the dial host.
// It is the heart of the request builder: it computes the payload hash, assembles
// the headers S3 requires (Host, x-amz-date, x-amz-content-sha256, optional
// x-amz-security-token), signs them with SigV4, attaches the Authorization
// header, and serialises the request through the net-http request model — the
// byte producer — leaving the socket to the Transport.
func (c *Client) build(o op) (host string, requestBytes []byte, err error) {
	now := c.now()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	host = c.host(o.bucket)
	path := c.path(o.bucket, o.key)
	payloadHash := HashPayload(o.body)

	// Assemble the headers to sign. Host, x-amz-content-sha256 and x-amz-date are
	// always signed; the user headers (Content-Type, storage class, metadata) and
	// the optional session token join them.
	signHeaders := [][2]string{
		{"host", host},
		{"x-amz-content-sha256", payloadHash},
		{"x-amz-date", amzDate},
	}
	signHeaders = append(signHeaders, o.headers...)
	if c.Credentials.SessionToken != "" {
		signHeaders = append(signHeaders, [2]string{"x-amz-security-token", c.Credentials.SessionToken})
	}

	signer := Signer{Credentials: c.Credentials, Region: c.Region}
	cr := CanonicalRequest{
		Method:       o.method,
		CanonicalURI: path,
		Query:        o.query,
		Headers:      signHeaders,
		PayloadHash:  payloadHash,
	}
	res := signer.Sign(cr, dateStamp, amzDate)

	// Build the wire request. The request-target carries the canonical query so
	// the server sees what we signed.
	target := path
	if q := canonicalQuery(o.query); q != "" {
		target += "?" + q
	}

	// The initial header list passed to net-http, in the order S3 SDKs send them.
	// net-http seeds Host from the authority argument; we pass the headers we
	// signed plus the Authorization header.
	initHeaders := [][2]string{
		{"x-amz-content-sha256", payloadHash},
		{"x-amz-date", amzDate},
	}
	for _, h := range o.headers {
		initHeaders = append(initHeaders, h)
	}
	if c.Credentials.SessionToken != "" {
		initHeaders = append(initHeaders, [2]string{"x-amz-security-token", c.Credentials.SessionToken})
	}
	initHeaders = append(initHeaders, [2]string{"authorization", res.Authorization})

	req, err := nethttp.NewRequest(o.method, target, host, initHeaders)
	if err != nil {
		return "", nil, err
	}
	if o.body != nil {
		req.SetBody(o.body)
	}
	requestBytes, err = serializeRequest(req)
	if err != nil {
		return "", nil, err
	}
	return host, requestBytes, nil
}

// serializeRequest renders the net-http request to its HTTP/1.1 wire bytes. It
// is a package-level seam so the (otherwise unreachable, since our request
// targets are always CR/LF-free) serialisation-error branch can be exercised.
var serializeRequest = func(req *nethttp.Request) ([]byte, error) {
	return req.Bytes("1.1")
}

// roundTrip builds, sends, and parses the HTTP response for an op. It returns the
// parsed response. The send is delegated to the Transport seam.
func (c *Client) roundTrip(o op) (*httpResponse, error) {
	if c.Transport == nil {
		return nil, errors.New("nets3: no Transport configured")
	}
	host, reqBytes, err := c.build(o)
	if err != nil {
		return nil, err
	}
	respBytes, err := c.Transport.RoundTrip(host, reqBytes)
	if err != nil {
		return nil, err
	}
	return parseHTTPResponse(respBytes)
}

// ----- operations -----

// GetObjectInput names the object to fetch and optional byte Range.
type GetObjectInput struct {
	Bucket string
	Key    string
	Range  string // optional HTTP Range header value, e.g. "bytes=0-9"
}

// GetObjectOutput is a fetched object: its body and useful headers.
type GetObjectOutput struct {
	Body          []byte
	ETag          string
	ContentType   string
	ContentLength int64
}

// GetObject fetches an object's bytes (S3 GET Object).
func (c *Client) GetObject(in GetObjectInput) (*GetObjectOutput, error) {
	var headers [][2]string
	if in.Range != "" {
		headers = append(headers, [2]string{"range", in.Range})
	}
	resp, err := c.roundTrip(op{method: "GET", bucket: in.Bucket, key: in.Key, headers: headers})
	if err != nil {
		return nil, err
	}
	if err := resp.s3Error(); err != nil {
		return nil, err
	}
	return &GetObjectOutput{
		Body:          resp.Body,
		ETag:          resp.header("etag"),
		ContentType:   resp.header("content-type"),
		ContentLength: parseInt64(resp.header("content-length")),
	}, nil
}

// PutObjectInput is an object to store: its bytes and optional metadata.
type PutObjectInput struct {
	Bucket       string
	Key          string
	Body         []byte
	ContentType  string
	StorageClass string // optional x-amz-storage-class, e.g. "REDUCED_REDUNDANCY"
}

// PutObjectOutput reports the stored object's ETag.
type PutObjectOutput struct {
	ETag string
}

// PutObject stores an object (S3 PUT Object).
func (c *Client) PutObject(in PutObjectInput) (*PutObjectOutput, error) {
	var headers [][2]string
	if in.ContentType != "" {
		headers = append(headers, [2]string{"content-type", in.ContentType})
	}
	if in.StorageClass != "" {
		headers = append(headers, [2]string{"x-amz-storage-class", in.StorageClass})
	}
	body := in.Body
	if body == nil {
		body = []byte{}
	}
	resp, err := c.roundTrip(op{method: "PUT", bucket: in.Bucket, key: in.Key, headers: headers, body: body})
	if err != nil {
		return nil, err
	}
	if err := resp.s3Error(); err != nil {
		return nil, err
	}
	return &PutObjectOutput{ETag: resp.header("etag")}, nil
}

// DeleteObject removes an object (S3 DELETE Object).
func (c *Client) DeleteObject(bucket, key string) error {
	resp, err := c.roundTrip(op{method: "DELETE", bucket: bucket, key: key})
	if err != nil {
		return err
	}
	return resp.s3Error()
}

// HeadObjectOutput is the metadata returned by a HEAD Object.
type HeadObjectOutput struct {
	ETag          string
	ContentType   string
	ContentLength int64
	LastModified  string
}

// HeadObject fetches an object's metadata without its body (S3 HEAD Object).
func (c *Client) HeadObject(bucket, key string) (*HeadObjectOutput, error) {
	resp, err := c.roundTrip(op{method: "HEAD", bucket: bucket, key: key})
	if err != nil {
		return nil, err
	}
	if err := resp.s3Error(); err != nil {
		return nil, err
	}
	return &HeadObjectOutput{
		ETag:          resp.header("etag"),
		ContentType:   resp.header("content-type"),
		ContentLength: parseInt64(resp.header("content-length")),
		LastModified:  resp.header("last-modified"),
	}, nil
}

// ListObjectsV2Input parameters the ListObjectsV2 call.
type ListObjectsV2Input struct {
	Bucket            string
	Prefix            string
	Delimiter         string
	MaxKeys           int    // 0 means omit (server default)
	ContinuationToken string // from a prior truncated page
}

// ListObjectsV2 lists objects in a bucket (S3 ListObjectsV2). It returns the
// parsed object listing including the next continuation token when truncated.
func (c *Client) ListObjectsV2(in ListObjectsV2Input) (*ListObjectsV2Output, error) {
	query := [][2]string{{"list-type", "2"}}
	if in.Prefix != "" {
		query = append(query, [2]string{"prefix", in.Prefix})
	}
	if in.Delimiter != "" {
		query = append(query, [2]string{"delimiter", in.Delimiter})
	}
	if in.MaxKeys > 0 {
		query = append(query, [2]string{"max-keys", strconv.Itoa(in.MaxKeys)})
	}
	if in.ContinuationToken != "" {
		query = append(query, [2]string{"continuation-token", in.ContinuationToken})
	}
	resp, err := c.roundTrip(op{method: "GET", bucket: in.Bucket, query: query})
	if err != nil {
		return nil, err
	}
	if err := resp.s3Error(); err != nil {
		return nil, err
	}
	return parseListObjectsV2(resp.Body)
}

// ListBuckets lists all buckets owned by the caller (S3 ListBuckets / GET
// Service). It targets the regional service endpoint with no bucket.
func (c *Client) ListBuckets() (*ListBucketsOutput, error) {
	resp, err := c.roundTrip(op{method: "GET", bucket: ""})
	if err != nil {
		return nil, err
	}
	if err := resp.s3Error(); err != nil {
		return nil, err
	}
	return parseListBuckets(resp.Body)
}

// CreateBucket creates a bucket (S3 PUT Bucket). For any region other than
// us-east-1, S3 requires a CreateBucketConfiguration body naming the
// LocationConstraint; this builds it automatically.
func (c *Client) CreateBucket(bucket string) error {
	var body []byte
	if c.Region != "" && c.Region != "us-east-1" {
		body = []byte("<CreateBucketConfiguration xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\">" +
			"<LocationConstraint>" + xmlEscape(c.Region) + "</LocationConstraint>" +
			"</CreateBucketConfiguration>")
	}
	o := op{method: "PUT", bucket: bucket}
	if body != nil {
		o.body = body
		o.headers = [][2]string{{"content-type", "application/xml"}}
	}
	resp, err := c.roundTrip(o)
	if err != nil {
		return err
	}
	return resp.s3Error()
}

// DeleteBucket deletes an empty bucket (S3 DELETE Bucket).
func (c *Client) DeleteBucket(bucket string) error {
	resp, err := c.roundTrip(op{method: "DELETE", bucket: bucket})
	if err != nil {
		return err
	}
	return resp.s3Error()
}

// parseInt64 parses a decimal int64, returning 0 on any error (used for
// optional numeric headers).
func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// xmlEscape escapes the five XML predefined entities for the small bodies this
// package builds (LocationConstraint).
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}
