// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import (
	"strconv"
	"strings"

	"github.com/go-ruby-rexml/rexml"
)

// Error is an S3 error response (the <Error><Code><Message>…</Error> body), or
// an HTTP-level error when the body could not be parsed. It implements the Go
// error interface so a Ruby caller (via rbgo) can raise it as an S3::Error.
type Error struct {
	StatusCode int    // HTTP status (set by the response layer)
	Code       string // S3 error code, e.g. "NoSuchKey"
	Message    string // human-readable message
	RequestID  string // x-amz-request-id echoed in the body, if present
	Resource   string // the offending resource, if present
}

// Error renders the S3 error as "Code: Message (status N)".
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Code)
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.StatusCode != 0 {
		b.WriteString(" (status ")
		b.WriteString(strconv.Itoa(e.StatusCode))
		b.WriteString(")")
	}
	return b.String()
}

// childText returns the text of the first direct child element named name, or "".
func childText(e *rexml.Element, name string) string {
	if c := e.FirstElement(name); c != nil {
		return c.Text()
	}
	return ""
}

// parseS3Error parses an S3 <Error> document. It returns nil when the body is
// not a parseable <Error> element (so the caller can fall back to the HTTP
// status).
func parseS3Error(body []byte) *Error {
	doc, err := rexml.Parse(string(body))
	if err != nil {
		return nil
	}
	root := doc.Root()
	if root == nil || root.Name != "Error" {
		return nil
	}
	return &Error{
		Code:      childText(root, "Code"),
		Message:   childText(root, "Message"),
		RequestID: childText(root, "RequestId"),
		Resource:  childText(root, "Resource"),
	}
}

// Object is one entry in a ListObjectsV2 result.
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified string
	StorageClass string
}

// CommonPrefix is a "directory" rolled up by a delimiter in ListObjectsV2.
type CommonPrefix struct {
	Prefix string
}

// ListObjectsV2Output is the parsed object listing.
type ListObjectsV2Output struct {
	Name                  string
	Prefix                string
	Delimiter             string
	MaxKeys               int
	KeyCount              int
	IsTruncated           bool
	ContinuationToken     string
	NextContinuationToken string
	Contents              []Object
	CommonPrefixes        []CommonPrefix
}

// parseListObjectsV2 parses a ListBucketResult document.
func parseListObjectsV2(body []byte) (*ListObjectsV2Output, error) {
	doc, err := rexml.Parse(string(body))
	if err != nil {
		return nil, &Error{Code: "MalformedXML", Message: err.Error()}
	}
	root := doc.Root()
	if root == nil || root.Name != "ListBucketResult" {
		return nil, &Error{Code: "MalformedXML", Message: "expected <ListBucketResult> root"}
	}
	out := &ListObjectsV2Output{
		Name:                  childText(root, "Name"),
		Prefix:                childText(root, "Prefix"),
		Delimiter:             childText(root, "Delimiter"),
		MaxKeys:               atoiSafe(childText(root, "MaxKeys")),
		KeyCount:              atoiSafe(childText(root, "KeyCount")),
		IsTruncated:           childText(root, "IsTruncated") == "true",
		ContinuationToken:     childText(root, "ContinuationToken"),
		NextContinuationToken: childText(root, "NextContinuationToken"),
	}
	for _, c := range root.Elements("Contents") {
		out.Contents = append(out.Contents, Object{
			Key:          childText(c, "Key"),
			Size:         atoi64Safe(childText(c, "Size")),
			ETag:         childText(c, "ETag"),
			LastModified: childText(c, "LastModified"),
			StorageClass: childText(c, "StorageClass"),
		})
	}
	for _, c := range root.Elements("CommonPrefixes") {
		out.CommonPrefixes = append(out.CommonPrefixes, CommonPrefix{Prefix: childText(c, "Prefix")})
	}
	return out, nil
}

// Bucket is one bucket in a ListBuckets result.
type Bucket struct {
	Name         string
	CreationDate string
}

// ListBucketsOutput is the parsed ListBuckets result.
type ListBucketsOutput struct {
	OwnerID          string
	OwnerDisplayName string
	Buckets          []Bucket
}

// parseListBuckets parses a ListAllMyBucketsResult document.
func parseListBuckets(body []byte) (*ListBucketsOutput, error) {
	doc, err := rexml.Parse(string(body))
	if err != nil {
		return nil, &Error{Code: "MalformedXML", Message: err.Error()}
	}
	root := doc.Root()
	if root == nil || root.Name != "ListAllMyBucketsResult" {
		return nil, &Error{Code: "MalformedXML", Message: "expected <ListAllMyBucketsResult> root"}
	}
	out := &ListBucketsOutput{}
	if owner := root.FirstElement("Owner"); owner != nil {
		out.OwnerID = childText(owner, "ID")
		out.OwnerDisplayName = childText(owner, "DisplayName")
	}
	if buckets := root.FirstElement("Buckets"); buckets != nil {
		for _, b := range buckets.Elements("Bucket") {
			out.Buckets = append(out.Buckets, Bucket{
				Name:         childText(b, "Name"),
				CreationDate: childText(b, "CreationDate"),
			})
		}
	}
	return out, nil
}

// atoiSafe parses a decimal int, returning 0 on error.
func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// atoi64Safe parses a decimal int64, returning 0 on error.
func atoi64Safe(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
