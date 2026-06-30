// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import "testing"

// A canned ListObjectsV2 response from the S3 API reference, truncated to two
// objects plus a common prefix, with the truncation/continuation tokens.
const listObjectsV2XML = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>example-bucket</Name>
  <Prefix>photos/</Prefix>
  <Delimiter>/</Delimiter>
  <MaxKeys>2</MaxKeys>
  <KeyCount>2</KeyCount>
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>1ueGcxLPRx1Tr/XYZ</NextContinuationToken>
  <Contents>
    <Key>photos/2006/January/sample.jpg</Key>
    <LastModified>2006-01-01T12:00:00.000Z</LastModified>
    <ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>
    <Size>142863</Size>
    <StorageClass>STANDARD</StorageClass>
  </Contents>
  <Contents>
    <Key>photos/2006/February/sample2.jpg</Key>
    <LastModified>2006-02-01T12:00:00.000Z</LastModified>
    <ETag>&quot;abcabcabc1234567890abcabcabc1234&quot;</ETag>
    <Size>0</Size>
    <StorageClass>GLACIER</StorageClass>
  </Contents>
  <CommonPrefixes>
    <Prefix>photos/2007/</Prefix>
  </CommonPrefixes>
</ListBucketResult>`

func TestParseListObjectsV2(t *testing.T) {
	out, err := parseListObjectsV2([]byte(listObjectsV2XML))
	if err != nil {
		t.Fatalf("parseListObjectsV2 error: %v", err)
	}
	if out.Name != "example-bucket" || out.Prefix != "photos/" || out.Delimiter != "/" {
		t.Errorf("header fields wrong: %+v", out)
	}
	if out.MaxKeys != 2 || out.KeyCount != 2 || !out.IsTruncated {
		t.Errorf("count/truncation wrong: %+v", out)
	}
	if out.NextContinuationToken != "1ueGcxLPRx1Tr/XYZ" {
		t.Errorf("next token = %q", out.NextContinuationToken)
	}
	if len(out.Contents) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(out.Contents))
	}
	o0 := out.Contents[0]
	if o0.Key != "photos/2006/January/sample.jpg" || o0.Size != 142863 ||
		o0.ETag != `"d41d8cd98f00b204e9800998ecf8427e"` || o0.StorageClass != "STANDARD" ||
		o0.LastModified != "2006-01-01T12:00:00.000Z" {
		t.Errorf("object[0] wrong: %+v", o0)
	}
	if out.Contents[1].Size != 0 || out.Contents[1].StorageClass != "GLACIER" {
		t.Errorf("object[1] wrong: %+v", out.Contents[1])
	}
	if len(out.CommonPrefixes) != 1 || out.CommonPrefixes[0].Prefix != "photos/2007/" {
		t.Errorf("common prefixes wrong: %+v", out.CommonPrefixes)
	}
}

func TestParseListObjectsV2Errors(t *testing.T) {
	if _, err := parseListObjectsV2([]byte("<<<not xml")); err == nil {
		t.Error("expected parse error for malformed XML")
	}
	if _, err := parseListObjectsV2([]byte("<Other/>")); err == nil {
		t.Error("expected error for wrong root element")
	}
}

const listBucketsXML = `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Owner>
    <ID>bcaf1ffd86f461ca5fb16fd081034f</ID>
    <DisplayName>webfile</DisplayName>
  </Owner>
  <Buckets>
    <Bucket>
      <Name>quotes</Name>
      <CreationDate>2006-02-03T16:45:09.000Z</CreationDate>
    </Bucket>
    <Bucket>
      <Name>samples</Name>
      <CreationDate>2006-02-03T16:41:58.000Z</CreationDate>
    </Bucket>
  </Buckets>
</ListAllMyBucketsResult>`

func TestParseListBuckets(t *testing.T) {
	out, err := parseListBuckets([]byte(listBucketsXML))
	if err != nil {
		t.Fatalf("parseListBuckets error: %v", err)
	}
	if out.OwnerID != "bcaf1ffd86f461ca5fb16fd081034f" || out.OwnerDisplayName != "webfile" {
		t.Errorf("owner wrong: %+v", out)
	}
	if len(out.Buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(out.Buckets))
	}
	if out.Buckets[0].Name != "quotes" || out.Buckets[0].CreationDate != "2006-02-03T16:45:09.000Z" {
		t.Errorf("bucket[0] wrong: %+v", out.Buckets[0])
	}
	if out.Buckets[1].Name != "samples" {
		t.Errorf("bucket[1] wrong: %+v", out.Buckets[1])
	}
}

func TestParseListBucketsErrors(t *testing.T) {
	if _, err := parseListBuckets([]byte("<<<")); err == nil {
		t.Error("expected parse error")
	}
	if _, err := parseListBuckets([]byte("<Nope/>")); err == nil {
		t.Error("expected wrong-root error")
	}
	// Owner / Buckets absent: still valid, empty result.
	out, err := parseListBuckets([]byte(`<ListAllMyBucketsResult/>`))
	if err != nil || len(out.Buckets) != 0 || out.OwnerID != "" {
		t.Errorf("empty result = %+v, %v", out, err)
	}
}

const s3ErrorXML = `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>NoSuchKey</Code>
  <Message>The specified key does not exist.</Message>
  <Key>missing.txt</Key>
  <Resource>/example-bucket/missing.txt</Resource>
  <RequestId>4442587FB7D0A2F9</RequestId>
</Error>`

func TestParseS3Error(t *testing.T) {
	e := parseS3Error([]byte(s3ErrorXML))
	if e == nil {
		t.Fatal("expected an error")
	}
	if e.Code != "NoSuchKey" || e.Message != "The specified key does not exist." ||
		e.Resource != "/example-bucket/missing.txt" || e.RequestID != "4442587FB7D0A2F9" {
		t.Errorf("parsed error wrong: %+v", e)
	}
	// Error() rendering with and without status.
	e.StatusCode = 404
	if got := e.Error(); got != "NoSuchKey: The specified key does not exist. (status 404)" {
		t.Errorf("Error() = %q", got)
	}
	bare := &Error{Code: "AccessDenied"}
	if got := bare.Error(); got != "AccessDenied" {
		t.Errorf("bare Error() = %q", got)
	}
}

func TestParseS3ErrorNotAnError(t *testing.T) {
	if parseS3Error([]byte("<<<")) != nil {
		t.Error("malformed XML should yield nil")
	}
	if parseS3Error([]byte("<NotError/>")) != nil {
		t.Error("non-Error root should yield nil")
	}
}

func TestChildTextMissing(t *testing.T) {
	// A ListBucketResult whose Contents lack optional fields exercises childText's
	// "missing child" branch.
	xml := `<ListBucketResult><Contents><Key>k</Key></Contents></ListBucketResult>`
	out, err := parseListObjectsV2([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if out.Contents[0].Key != "k" || out.Contents[0].Size != 0 || out.Contents[0].ETag != "" {
		t.Errorf("missing-field object = %+v", out.Contents[0])
	}
}

func TestXMLEscape(t *testing.T) {
	if got := xmlEscape(`a&b<c>d"e'f`); got != "a&amp;b&lt;c&gt;d&quot;e&apos;f" {
		t.Errorf("xmlEscape = %q", got)
	}
}

func TestAtoiSafe(t *testing.T) {
	if atoiSafe("nope") != 0 || atoi64Safe("nope") != 0 {
		t.Error("bad numbers should be 0")
	}
	if atoiSafe(" 7 ") != 7 || atoi64Safe(" 9 ") != 9 {
		t.Error("trimmed numbers should parse")
	}
}
