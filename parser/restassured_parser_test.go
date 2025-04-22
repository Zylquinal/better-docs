package parser

import (
	"bufio"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
)

const mockRestAssuredLog = `Request method: POST
Request URI: http://example.com/api/test?foo=bar&baz=qux
Headers:
    Content-Type=application/json
    Accept=application/json
Body:
    {"key":"value"}
`

func TestParseLog(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(mockRestAssuredLog))
	pr, err := ParseLog(r)
	if err != nil {
		t.Fatalf("ParseLog returned error: %v", err)
	}

	if pr.Method != "POST" {
		t.Errorf("Expected Method POST, got %q", pr.Method)
	}
	if pr.URI != "http://example.com/api/test?foo=bar&baz=qux" {
		t.Errorf("Expected URI http://example.com/api/test?foo=bar&baz=qux, got %q", pr.URI)
	}

	expectedHeaders := textproto.MIMEHeader{
		"Content-Type": {"application/json"},
		"Accept":       {"application/json"},
	}

	if !reflect.DeepEqual(pr.Headers, expectedHeaders) {
		t.Errorf("Headers mismatch.\nExpected: %#v\nGot:      %#v", expectedHeaders, pr.Headers)
	}

	expectedBody := `{"key":"value"}`
	if string(pr.Body) != expectedBody {
		t.Errorf("Expected Body %q, got %q", expectedBody, string(pr.Body))
	}
}
