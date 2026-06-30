<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-net-s3/brand/main/social/go-ruby-net-s3-net-s3.png" alt="go-ruby-net-s3/net-s3" width="720"></p>

# net-s3 — go-ruby-net-s3

[![Docs](https://img.shields.io/badge/docs-mkdocs--material-DC2626)](https://go-ruby-net-s3.github.io/docs/)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) Ruby-style Amazon S3 client core** — AWS **Signature Version
4** request signing, S3 **REST request building**, and S3 **XML response parsing**.
It produces a fully-signed HTTP/1.1 request as bytes and decodes the response,
leaving only the actual HTTPS transport (the TCP/TLS connect + send + recv) to a
host-supplied seam. Everything this library does is deterministic, network-free,
and CGO-free.

It is designed to be the S3 backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby) (`rbgo`), but is a
**standalone, reusable** module — a sibling of
[go-ruby-net-http](https://github.com/go-ruby-net-http/net-http) (the request
byte-producer it delegates to), [go-ruby-rexml](https://github.com/go-ruby-rexml/rexml)
(the XML parser it reads responses with), and
[go-ruby-digest](https://github.com/go-ruby-digest/digest).

> **What it is — and isn't.** Building and signing an S3 request, and parsing the
> response XML, is fully deterministic and needs **no network and no interpreter**,
> so it lives here as pure Go. The actual HTTPS call — opening the TLS socket,
> writing the request bytes, reading the response — is the **host's job**: the
> library hands the signed request bytes to a `Transport` you supply (rbgo wires
> one that does the real `connect`/`send`/`recv`) and parses whatever bytes come
> back. This is the same "pure-compute core, host seam for I/O" split as the rest
> of the go-ruby-* family.

## Features

- **AWS Signature Version 4**, validated **byte-for-byte against AWS's published
  S3 example vectors** (the GET-object Range example and the PUT-object example):
  the canonical request, the string-to-sign (`AWS4-HMAC-SHA256` + the
  `date/region/s3/aws4_request` scope), the signing-key derivation chain
  (`kDate → kRegion → kService → kSigning`), the signature, and the
  `Authorization` header.
- **S3 REST operations** — `GetObject`, `PutObject`, `DeleteObject`,
  `HeadObject`, `ListObjectsV2` (with pagination tokens), `ListBuckets`,
  `CreateBucket` (auto `LocationConstraint` outside us-east-1), `DeleteBucket` —
  each building the method, path, query, and required headers
  (`x-amz-content-sha256`, `x-amz-date`, `Host`, optional
  `x-amz-security-token`), signing them, and serialising through the
  go-ruby-net-http request model.
- **Virtual-hosted and path-style** addressing, plus a custom `Endpoint` override
  (MinIO and other S3-compatible servers).
- **XML response parsing** via go-ruby-rexml — `ListObjectsV2`
  (`<Contents><Key><Size><ETag>…`), `ListBuckets`, and the S3
  `<Error><Code><Message>…</Error>` body decoded into a typed `*Error`.
- **Temporary (STS) credentials** via a session token, signed correctly.

CGO-free, **100% test coverage**, `gofmt` + `go vet` clean, and green across the
six 64-bit Go targets (amd64, arm64, riscv64, loong64, ppc64le, s390x) and three
OSes (Linux, macOS, Windows). The tests are deterministic and ruby-free (the AWS
vectors + canned S3 XML, with the transport stubbed) — no network is touched.

## Install

```sh
go get github.com/go-ruby-net-s3/net-s3
```

## Usage

```go
package main

import (
	"crypto/tls"
	"errors"
	"fmt"

	nets3 "github.com/go-ruby-net-s3/net-s3"
)

// tlsTransport is the host seam: it does the real TLS connect + send + recv.
type tlsTransport struct{}

func (tlsTransport) RoundTrip(host string, req []byte) ([]byte, error) {
	conn, err := tls.Dial("tcp", host+":443", &tls.Config{ServerName: host})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 64<<10)
	tmp := make([]byte, 32<<10)
	for {
		n, err := conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil { // io.EOF at end of the (Connection: close) response
			break
		}
	}
	return buf, nil
}

func main() {
	creds := nets3.NewCredentials("AKIA…", "wJalr…")
	c := nets3.NewClient("us-east-1", creds, tlsTransport{})

	// Upload.
	if _, err := c.PutObject(nets3.PutObjectInput{
		Bucket: "examplebucket", Key: "hello.txt",
		Body: []byte("hello"), ContentType: "text/plain",
	}); err != nil {
		panic(err)
	}

	// List.
	list, err := c.ListObjectsV2(nets3.ListObjectsV2Input{Bucket: "examplebucket"})
	if err != nil {
		panic(err)
	}
	for _, o := range list.Contents {
		fmt.Printf("%s (%d bytes) %s\n", o.Key, o.Size, o.ETag)
	}

	// Fetch. A 4xx/5xx is returned as a typed *nets3.Error.
	obj, err := c.GetObject(nets3.GetObjectInput{Bucket: "examplebucket", Key: "hello.txt"})
	if err != nil {
		var e *nets3.Error
		if errors.As(err, &e) {
			fmt.Printf("S3 error %s: %s\n", e.Code, e.Message)
		}
		panic(err)
	}
	fmt.Println(string(obj.Body))
}
```

### Just the signing

The SigV4 core is exported directly, so you can sign any canonical request:

```go
signer := nets3.Signer{Credentials: creds, Region: "us-east-1"}
res := signer.Sign(nets3.CanonicalRequest{
	Method:       "GET",
	CanonicalURI: "/test.txt",
	Headers: [][2]string{
		{"host", "examplebucket.s3.amazonaws.com"},
		{"range", "bytes=0-9"},
		{"x-amz-content-sha256", nets3.EmptyPayloadHash},
		{"x-amz-date", "20130524T000000Z"},
	},
	PayloadHash: nets3.EmptyPayloadHash,
}, "20130524", "20130524T000000Z")
fmt.Println(res.Authorization) // ready for the Authorization header
```

## The HTTPS transport seam

`Client` never opens a socket. It builds and signs the request, hands the bytes to
your `Transport.RoundTrip(host, requestBytes)`, and parses the response bytes you
return. This keeps the library deterministic and testable (the tests use a fake
transport) and lets the host — `rbgo`, or your own program — own the TLS, the
connection pool, retries, and timeouts.

```go
type Transport interface {
	RoundTrip(host string, requestBytes []byte) (responseBytes []byte, err error)
}
```

## Tests & coverage

```sh
go test -race -cover ./...
```

The suite is deterministic and ruby-free: the AWS-published SigV4 vectors
(asserted byte-for-byte) and canned S3 XML responses, with the HTTPS transport
stubbed. Coverage is **100%** of statements and is gated in CI on Linux, macOS,
and Windows, plus the six 64-bit architectures.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright (c) 2026, the
go-ruby-net-s3/net-s3 authors.
