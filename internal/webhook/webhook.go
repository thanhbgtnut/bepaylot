// Package webhook delivers outbound HTTP callbacks: a JSON POST with event
// and delivery headers, an optional HMAC-SHA256 signature, no redirects, and
// (unless allowed) no connections to loopback, private or link-local
// addresses — checked on the resolved IP at dial time, so DNS tricks cannot
// reach internal services (SSRF).
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Header names sent with every delivery.
const (
	HeaderEvent     = "X-Bepaylot-Event"
	HeaderDelivery  = "X-Bepaylot-Delivery"
	HeaderAttempt   = "X-Bepaylot-Attempt"
	HeaderTimestamp = "X-Bepaylot-Timestamp"
	HeaderSignature = "X-Bepaylot-Signature"
)

// ErrPrivateAddress is returned for a destination on a blocked network.
var ErrPrivateAddress = errors.New("webhook: destination address is not allowed (loopback/private/link-local)")

// Config configures a Client.
type Config struct {
	Timeout      time.Duration
	Secret       string
	AllowPrivate bool
	UserAgent    string
}

// Client sends webhooks.
type Client struct {
	cfg Config
	hc  *http.Client
}

// New returns a client.
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "bepaylot-callback/1"
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if !cfg.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip, err := netip.ParseAddr(host); err == nil && blocked(ip) {
				return ErrPrivateAddress
			}
			return nil
		}
	}
	tr := &http.Transport{
		Proxy:                 nil, // a proxy would bypass the address check
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Client{cfg: cfg, hc: &http.Client{
		Timeout: cfg.Timeout, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// ValidateURL checks a callback URL before it is stored: absolute http(s),
// a host, no credentials, and — unless allowPrivate — not a literal blocked
// IP or "localhost". Hostnames are re-checked on every dial.
func ValidateURL(raw string, allowPrivate bool) error {
	if len(raw) > 2048 {
		return errors.New("callback_url is longer than 2048 characters")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("callback_url is not a valid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("callback_url must use http or https")
	}
	if u.Hostname() == "" {
		return errors.New("callback_url has no host")
	}
	if u.User != nil {
		return errors.New("callback_url must not contain credentials")
	}
	if allowPrivate {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ErrPrivateAddress
	}
	if ip, err := netip.ParseAddr(host); err == nil && blocked(ip) {
		return ErrPrivateAddress
	}
	return nil
}

func blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip) // carrier-grade NAT
}

// Delivery is one outbound call.
type Delivery struct {
	URL     string
	Event   string
	ID      string // stable across attempts: receivers deduplicate on it
	Attempt int
	Body    []byte
	SentAt  time.Time
}

// Result is the outcome of one attempt.
type Result struct {
	StatusCode int // 0 when no response was received
	Response   string
	Duration   time.Duration
	Err        error
}

// OK reports a 2xx answer.
func (r Result) OK() bool { return r.Err == nil && r.StatusCode >= 200 && r.StatusCode < 300 }

// Error describes a failed attempt.
func (r Result) Error() string {
	switch {
	case r.Err != nil:
		return r.Err.Error()
	case !r.OK():
		return fmt.Sprintf("HTTP %d", r.StatusCode)
	}
	return ""
}

// Sign returns the signature header value for a body sent at ts.
func Sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Send POSTs one delivery. It never returns an error: failures are in Result.
func (c *Client) Send(ctx context.Context, d Delivery) Result {
	t0 := time.Now()
	res := Result{}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(d.Body))
	if err != nil {
		res.Err = err
		return res
	}
	sent := d.SentAt
	if sent.IsZero() {
		sent = time.Now()
	}
	ts := sent.Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set(HeaderEvent, d.Event)
	req.Header.Set(HeaderDelivery, d.ID)
	req.Header.Set(HeaderAttempt, strconv.Itoa(d.Attempt))
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	if c.cfg.Secret != "" {
		req.Header.Set(HeaderSignature, Sign(c.cfg.Secret, ts, d.Body))
	}
	resp, err := c.hc.Do(req)
	res.Duration = time.Since(t0)
	if err != nil {
		if errors.Is(err, ErrPrivateAddress) {
			err = ErrPrivateAddress
		}
		res.Err = err
		return res
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	res.StatusCode, res.Response = resp.StatusCode, sanitize(b)
	res.Duration = time.Since(t0)
	return res
}

// sanitize keeps a response snippet storable as text.
func sanitize(b []byte) string {
	s := strings.ToValidUTF8(string(b), "�")
	s = strings.ReplaceAll(s, "\x00", "")
	if utf8.RuneCountInString(s) > 1000 {
		s = string([]rune(s)[:1000])
	}
	return s
}
