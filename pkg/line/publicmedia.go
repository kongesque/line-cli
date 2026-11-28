package line

// SSRF-hardened transport for external DOWNLOAD_URL media fetches.
// Adapted from the public-media network policy in beeper/line (#286).

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	publicMediaMaxRedirects   = 3
	publicMediaDefaultTimeout = 30 * time.Second
)

var (
	// ErrPublicMediaBlocked indicates that an external media URL or one of its
	// resolved IP addresses violates the public-media SSRF safety policy.
	ErrPublicMediaBlocked = errors.New("public media URL blocked by network policy")
	// ErrPublicMediaRedirectLimit indicates that an external media download
	// exceeded the maximum redirect hop count.
	ErrPublicMediaRedirectLimit = errors.New("public media exceeded redirect limit")
)

// PublicMediaStatusError reports a non-success HTTP status from an
// unauthenticated public OBS or external DOWNLOAD_URL fetch. It is
// intentionally distinct from ResponseError so an external server's
// HTTP 401/403 never triggers LINE token refresh or auth errors.
type PublicMediaStatusError struct {
	Status int
}

func (e *PublicMediaStatusError) Error() string {
	return fmt.Sprintf("public media download failed (status %d)", e.Status)
}

var (
	nat64WellKnownPrefix = netip.MustParsePrefix("64:ff9b::/96")

	publicMediaBlockedPrefixes = []netip.Prefix{
		// IPv4 non-public / reserved ranges
		netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
		netip.MustParsePrefix("10.0.0.0/8"),      // RFC 1918 private
		netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 CGNAT
		netip.MustParsePrefix("127.0.0.0/8"),     // loopback
		netip.MustParsePrefix("169.254.0.0/16"),  // link-local / cloud metadata
		netip.MustParsePrefix("172.16.0.0/12"),   // RFC 1918 private
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
		netip.MustParsePrefix("192.88.99.0/24"),  // deprecated 6to4 relay anycast
		netip.MustParsePrefix("192.168.0.0/16"),  // RFC 1918 private
		netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
		netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
		netip.MustParsePrefix("224.0.0.0/4"),     // multicast
		netip.MustParsePrefix("240.0.0.0/4"),     // reserved & broadcast

		// IPv6 non-public / reserved ranges
		netip.MustParsePrefix("::/128"),         // unspecified
		netip.MustParsePrefix("::1/128"),        // loopback
		netip.MustParsePrefix("64:ff9b:1::/48"), // RFC 8215 local-use NAT64
		netip.MustParsePrefix("100::/64"),       // RFC 6666 discard-only
		netip.MustParsePrefix("2001::/32"),      // Teredo
		netip.MustParsePrefix("2001:2::/48"),    // benchmarking
		netip.MustParsePrefix("2001:10::/28"),   // ORCHID
		netip.MustParsePrefix("2001:20::/28"),   // ORCHIDv2
		netip.MustParsePrefix("2001:db8::/32"),  // documentation
		netip.MustParsePrefix("2002::/16"),      // 6to4
		netip.MustParsePrefix("fc00::/7"),       // unique local
		netip.MustParsePrefix("fe80::/10"),      // link-local
		netip.MustParsePrefix("fec0::/10"),      // deprecated site-local
		netip.MustParsePrefix("ff00::/8"),       // multicast
	}
)

// isPublicMediaAddr returns true only when addr is a globally routable public
// unicast IPv4 or IPv6 address (and, for RFC 6052 NAT64 addresses, when its
// embedded IPv4 address is also public).
func isPublicMediaAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() {
		return false
	}
	if addr.Is6() && nat64WellKnownPrefix.Contains(addr) {
		raw := addr.As16()
		embedded := netip.AddrFrom4([4]byte{raw[12], raw[13], raw[14], raw[15]})
		return isPublicMediaAddr(embedded)
	}
	for _, prefix := range publicMediaBlockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// validatePublicMediaURL validates an absolute http(s) URL for external public
// media downloads and returns its normalized hostname and default port.
func validatePublicMediaURL(u *url.URL) (host string, port string, err error) {
	if u == nil {
		return "", "", fmt.Errorf("%w: missing URL", ErrPublicMediaBlocked)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http":
		port = "80"
	case "https":
		port = "443"
	default:
		return "", "", fmt.Errorf("%w: unsupported scheme", ErrPublicMediaBlocked)
	}
	if u.User != nil {
		return "", "", fmt.Errorf("%w: userinfo is not allowed", ErrPublicMediaBlocked)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", "", fmt.Errorf("%w: URL fragment is not allowed", ErrPublicMediaBlocked)
	}
	if rawPort := u.Port(); rawPort != "" && rawPort != port {
		return "", "", fmt.Errorf("%w: non-standard port is not allowed", ErrPublicMediaBlocked)
	}
	rawHost := strings.TrimSpace(u.Hostname())
	if rawHost == "" {
		return "", "", fmt.Errorf("%w: missing host", ErrPublicMediaBlocked)
	}
	if addr, parseErr := netip.ParseAddr(rawHost); parseErr == nil {
		if !isPublicMediaAddr(addr) {
			return "", "", fmt.Errorf("%w: non-public IP address", ErrPublicMediaBlocked)
		}
		return addr.Unmap().String(), port, nil
	}
	normalized := strings.ToLower(strings.TrimSuffix(rawHost, "."))
	if !isSafePublicHostname(normalized) {
		return "", "", fmt.Errorf("%w: invalid or private hostname", ErrPublicMediaBlocked)
	}
	return normalized, port, nil
}

func isSafePublicHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".localdomain") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}

func stripSensitivePublicMediaHeaders(h http.Header) {
	for _, key := range []string{
		"X-Line-Access",
		"X-Line-Application",
		"X-Line-Chrome-Version",
		"X-Line-ChannelToken",
		"X-Line-Mid",
		"X-Line-Album",
		"X-Talk-Meta",
		"X-Hmac",
		"Cookie",
		"Authorization",
		"Proxy-Authorization",
	} {
		h.Del(key)
	}
}

type publicMediaTransport struct {
	resolveIPs  func(ctx context.Context, host string) ([]netip.Addr, error)
	dialContext func(ctx context.Context, network, address string) (net.Conn, error)
	tlsConfig   *tls.Config
}

func newPublicMediaTransport() *publicMediaTransport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &publicMediaTransport{
		resolveIPs: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dialContext: dialer.DialContext,
	}
}

func (t *publicMediaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host, port, err := validatePublicMediaURL(req.URL)
	if err != nil {
		return nil, err
	}
	addrs, err := t.resolvePublicAddresses(req.Context(), host)
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if t.tlsConfig != nil {
		tlsCfg = t.tlsConfig.Clone()
	}
	tlsCfg.ServerName = host

	dialFn := t.dialContext
	if dialFn == nil {
		dialFn = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, addr := range addrs {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				conn, err := dialFn(ctx, network, net.JoinHostPort(addr.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1,
		IdleConnTimeout:       10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	outReq := req.Clone(req.Context())
	stripSensitivePublicMediaHeaders(outReq.Header)

	resp, err := tr.RoundTrip(outReq)
	if err != nil {
		tr.CloseIdleConnections()
		return nil, err
	}
	resp.Body = &transportClosingBody{ReadCloser: resp.Body, transport: tr}
	return resp, nil
}

func (t *publicMediaTransport) resolvePublicAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if !isPublicMediaAddr(addr) {
			return nil, fmt.Errorf("%w: non-public IP address", ErrPublicMediaBlocked)
		}
		return []netip.Addr{addr.Unmap()}, nil
	}
	resolve := t.resolveIPs
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	addrs, err := resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: host resolved to no addresses", ErrPublicMediaBlocked)
	}
	publicAddrs := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		if !isPublicMediaAddr(addr) {
			return nil, fmt.Errorf("%w: host resolved to non-public IP address", ErrPublicMediaBlocked)
		}
		publicAddrs = append(publicAddrs, addr.Unmap())
	}
	return publicAddrs, nil
}

type transportClosingBody struct {
	io.ReadCloser
	transport *http.Transport
}

func (b *transportClosingBody) Close() error {
	err := b.ReadCloser.Close()
	b.transport.CloseIdleConnections()
	return err
}

func (c *Client) publicMediaHTTPClient() *http.Client {
	timeout := publicMediaDefaultTimeout
	if obs := c.obsHTTPClient(); obs != nil && obs.Timeout > 0 {
		timeout = obs.Timeout
	}
	var transport http.RoundTripper = newPublicMediaTransport()
	if c.PublicMediaClient != nil {
		if c.PublicMediaClient.Timeout > 0 {
			timeout = c.PublicMediaClient.Timeout
		}
		if c.PublicMediaClient.Transport != nil {
			transport = c.PublicMediaClient.Transport
		}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > publicMediaMaxRedirects {
				return ErrPublicMediaRedirectLimit
			}
			if _, _, err := validatePublicMediaURL(req.URL); err != nil {
				return err
			}
			stripSensitivePublicMediaHeaders(req.Header)
			return nil
		},
	}
}
