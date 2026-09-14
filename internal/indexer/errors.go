package indexer

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Kind is what a failed request means for the source's health.
type Kind int

const (
	// KindOther: the request failed in a way waiting will not fix (a wrong
	// path, something that is not a feed). Recorded, not backed off.
	KindOther Kind = iota
	// KindCredentials: the source rejected the key. The source is disabled
	// until an admin fixes it — asking again only risks a ban.
	KindCredentials
	// KindRateLimited: the source is limiting us (HTTP 429, or its request or
	// download limit is reached). Backed off.
	KindRateLimited
	// KindUnavailable: 5xx, timeouts and connection failures. Backed off.
	KindUnavailable
)

// Error is a failed request to a search source. Msg is written for an operator
// and never contains the request URL or the API key.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// KindOf classifies any error a Client returned.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindUnavailable
	}
	return KindOther
}

func statusError(code int) *Error {
	switch {
	case code == 401 || code == 403:
		return &Error{Kind: KindCredentials, Msg: fmt.Sprintf("the source rejected the API key (HTTP %d)", code)}
	case code == 429:
		return &Error{Kind: KindRateLimited, Msg: "the source is limiting requests (HTTP 429)"}
	case code >= 500:
		return &Error{Kind: KindUnavailable, Msg: fmt.Sprintf("the source answered HTTP %d", code)}
	}
	return &Error{Kind: KindOther, Msg: fmt.Sprintf("the source answered HTTP %d", code)}
}

// transportError describes a failure to get an answer at all. net/http puts
// the full request URL — API key included — into its errors, so only the
// underlying cause is kept.
func transportError(err error) *Error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &Error{Kind: KindUnavailable, Msg: "the source did not answer in time"}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: KindUnavailable, Msg: "the request was cancelled"}
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return &Error{Kind: KindUnavailable, Msg: "the source could not be reached: " + clean(err.Error(), 200)}
}

// newznabError reads a newznab <error code="…" description="…"/> document, or
// returns nil when the body is not one.
//
// Codes (newznab API spec): 100–102 are credential problems (incorrect
// credentials, account suspended, insufficient privileges); 500 and 501 are
// the request and download limits; 900 is an unknown server error.
func newznabError(body []byte) *Error {
	head := bytes.TrimSpace(body)
	if len(head) > 512 {
		head = head[:512]
	}
	if !bytes.Contains(head, []byte("<error")) {
		return nil
	}
	se, ok := firstElement(body)
	if !ok || se.Name.Local != "error" {
		return nil
	}
	var code int
	var desc string
	for _, a := range se.Attr {
		switch a.Name.Local {
		case "code":
			code, _ = strconv.Atoi(strings.TrimSpace(a.Value))
		case "description":
			desc = clean(a.Value, 160)
		}
	}
	msg := "the source refused the request"
	if desc != "" {
		msg += ": " + desc
	}
	if code != 0 {
		msg += fmt.Sprintf(" (code %d)", code)
	}
	switch {
	case code >= 100 && code <= 102:
		return &Error{Kind: KindCredentials, Msg: msg}
	case code == 500 || code == 501:
		return &Error{Kind: KindRateLimited, Msg: msg}
	case code == 900:
		return &Error{Kind: KindUnavailable, Msg: msg}
	}
	return &Error{Kind: KindOther, Msg: msg}
}

// redacted removes the API key from an error's message, in case a source (or
// an address in a dial error) echoed it.
func redacted(e *Error, key string) *Error {
	if key != "" && strings.Contains(e.Msg, key) {
		e.Msg = strings.ReplaceAll(e.Msg, key, "…")
	}
	return e
}

// clean keeps a remote-supplied string printable and short.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > n {
		cut := n
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

// newDecoder reads a feed, caps or error document, accepting the Latin-1 and
// Windows-1252 declarations some sources still send alongside UTF-8.
func newDecoder(body []byte) *xml.Decoder {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return input, nil
		case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
			return &latin1Reader{r: input}, nil
		}
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	return dec
}

// rootElement advances dec to the document's root element.
func rootElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se, nil
		}
	}
}

// firstElement returns the document's root element.
func firstElement(body []byte) (xml.StartElement, bool) {
	se, err := rootElement(newDecoder(body))
	return se, err == nil
}

// latin1Reader maps single-byte Latin-1 input to UTF-8. (The few Windows-1252
// punctuation marks in 0x80–0x9F come out as their Latin-1 control code
// points; titles do not depend on them.)
type latin1Reader struct {
	r       io.Reader
	pending []byte
	err     error
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(l.pending) == 0 {
		if l.err != nil {
			return 0, l.err
		}
		buf := make([]byte, max(len(p)/2, 1))
		k, err := l.r.Read(buf)
		l.err = err
		out := make([]byte, 0, 2*k)
		for _, b := range buf[:k] {
			if b < 0x80 {
				out = append(out, b)
			} else {
				out = append(out, 0xC0|b>>6, 0x80|b&0x3F)
			}
		}
		l.pending = out
		if len(out) == 0 {
			return 0, l.err
		}
	}
	n := copy(p, l.pending)
	l.pending = l.pending[n:]
	return n, nil
}
