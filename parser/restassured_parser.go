package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

type State int

const (
	StateNone State = iota
	StateHeaders
	StateBody
)

type ParsedRequest struct {
	Method     string
	URI        string
	Headers    textproto.MIMEHeader
	Params     url.Values
	Body       []byte
	PathParams map[string]string
}

type ParsedResponse struct {
	Proto   string
	Status  string
	Headers textproto.MIMEHeader
	Body    []byte
}

func ParseLog(r *bufio.Reader) (ParsedRequest, error) {
	var pr ParsedRequest
	pr.Headers = textproto.MIMEHeader{}
	pr.Params = url.Values{}
	var bodyLines []string
	state := StateNone

	for {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return pr, err
		}
		trim := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trim, "Request method:"):
			pr.Method = strings.TrimSpace(strings.TrimPrefix(trim, "Request method:"))
		case strings.HasPrefix(trim, "Request URI:"):
			pr.URI = strings.TrimSpace(strings.TrimPrefix(trim, "Request URI:"))
			if u, err := url.Parse(pr.URI); err == nil {
				pr.Params = u.Query()
			}
		case strings.HasPrefix(trim, "Headers:"):
			rest := strings.TrimSpace(strings.TrimPrefix(trim, "Headers:"))
			if rest != "" && rest != "<none>" {
				parts := strings.SplitN(rest, "=", 2)
				if len(parts) == 2 {
					key := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(parts[0]))
					pr.Headers.Add(key, strings.TrimSpace(parts[1]))
				}
			}
			state = StateHeaders
		case strings.HasPrefix(trim, "Body:"):
			c := strings.TrimSpace(strings.TrimPrefix(trim, "Body:"))
			if c != "" && c != "<none>" {
				bodyLines = append(bodyLines, c)
			}
			state = StateBody
		case state == StateHeaders:
			if trim == "" {
				state = StateNone
			} else {
				parts := strings.SplitN(trim, "=", 2)
				if len(parts) == 2 {
					pr.Headers.Add(
						textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(parts[0])),
						strings.TrimSpace(parts[1]),
					)
				}
			}
		case state == StateBody:
			if trim == "" || strings.HasPrefix(trim, "Response") {
				state = StateNone
			} else {
				bodyLines = append(bodyLines, trim)
			}
		}

		if err == io.EOF {
			break
		}
	}

	pr.Body = []byte(strings.Join(bodyLines, "\n"))
	if pr.Method == "" || pr.URI == "" {
		return pr, fmt.Errorf("missing method or URI")
	}
	return pr, nil
}

func DoRequest(pr ParsedRequest) (ParsedResponse, error) {
	var pres ParsedResponse
	client := http.DefaultClient

	req, err := http.NewRequest(pr.Method, pr.URI, bytes.NewReader(pr.Body))
	if err != nil {
		return pres, err
	}
	for k, vs := range pr.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return pres, err
	}
	defer resp.Body.Close()

	pres.Proto = resp.Proto
	pres.Status = resp.Status
	pres.Headers = textproto.MIMEHeader(resp.Header)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return pres, err
	}
	pres.Body = body
	return pres, nil
}

type Printer struct{}

func (Printer) PrettyRequest(pr ParsedRequest) {
	fmt.Print(RequestString(pr))
}

func (Printer) PrettyResponse(pres ParsedResponse) {
	fmt.Print(ResponseString(pres))
}

func RequestString(pr ParsedRequest) string {
	var out strings.Builder
	write := func(format string, args ...interface{}) {
		fmt.Fprintf(&out, format+"\n", args...)
	}

	write("Request method: %s", pr.Method)
	write("Request URI: %s", pr.URI)
	write("Proxy: <none>")

	// Render captured query params
	if len(pr.Params) == 0 {
		write("Request params: <none>")
	} else {
		write("Request params:")
		for k, vs := range pr.Params {
			for _, v := range vs {
				write("    %s=%s", k, v)
			}
		}
	}

	write("Form params: <none>")
	write("Path params: <none>")

	if len(pr.Headers) == 0 {
		write("Headers: <none>")
	} else {
		write("Headers:")
		for k, vs := range pr.Headers {
			for _, v := range vs {
				write("    %s=%s", k, v)
			}
		}
	}

	write("Cookies: <none>")
	write("Multiparts: <none>")

	if len(pr.Body) == 0 {
		write("Body: <none>")
	} else {
		contentType := pr.Headers.Get("Content-Type")
		write("Body:")
		if strings.Contains(contentType, "application/json") {
			var buf bytes.Buffer
			if err := json.Indent(&buf, pr.Body, "", "    "); err == nil {
				write(buf.String())
			} else {
				write(string(pr.Body))
			}
		} else {
			write(string(pr.Body))
		}
	}

	return out.String()
}

// ResponseString builds a string representation of the ParsedResponse.
func ResponseString(pres ParsedResponse) string {
	var out strings.Builder
	write := func(format string, args ...interface{}) {
		fmt.Fprintf(&out, format+"\n", args...)
	}

	write("")
	write("Response :")
	write("%s %s", pres.Proto, pres.Status)

	if len(pres.Headers) == 0 {
		write("Headers: <none>")
	} else {
		write("Headers:")
		for k, vs := range pres.Headers {
			for _, v := range vs {
				write("    %s=%s", k, v)
			}
		}
	}

	write("Cookies: <none>")
	write("Multiparts: <none>")

	if len(pres.Body) == 0 {
		write("Body: <none>")
	} else {
		contentType := pres.Headers.Get("Content-Type")
		write("Body:")
		if strings.Contains(contentType, "application/json") {
			var buf bytes.Buffer
			if err := json.Indent(&buf, pres.Body, "", "    "); err == nil {
				write(buf.String())
			} else {
				write(string(pres.Body))
			}
		} else {
			write(string(pres.Body))
		}
	}

	return out.String()
}
