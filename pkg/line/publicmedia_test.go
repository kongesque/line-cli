package line

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestIsPublicMediaAddrBlocklist(t *testing.T) {
	blocked := []string{
		"0.0.0.0",
		"0.1.2.3",
		"10.0.0.1",
		"100.64.0.1",
		"100.127.255.254",
		"127.0.0.1",
		"127.255.255.254",
		"169.254.169.254",
		"172.16.0.1",
		"172.31.255.254",
		"192.0.0.1",
		"192.0.2.1",
		"192.88.99.1",
		"192.168.1.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"255.255.255.255",
		"::",
		"::1",
		"::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
		"::ffff:169.254.169.254",
		"64:ff9b::10.0.0.1",
		"64:ff9b::127.0.0.1",
		"64:ff9b::169.254.169.254",
		"64:ff9b::192.168.0.1",
		"64:ff9b:1::1",
		"100::1",
		"2001::1",
		"2001:2::1",
		"2001:10::1",
		"2001:20::1",
		"2001:db8::1",
		"2002:c000:0201::1",
		"fc00::1",
		"fd12:3456:789a::1",
		"fe80::1",
		"fe80::1%eth0",
		"fec0::1",
		"ff02::1",
	}
	for _, raw := range blocked {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if isPublicMediaAddr(addr) {
			t.Errorf("expected %s to be blocked", raw)
		}
	}

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34",
		"::ffff:93.184.216.34",
		"64:ff9b::93.184.216.34",
		"2606:2800:220:1:248:1893:25c8:1946",
	}
	for _, raw := range allowed {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if !isPublicMediaAddr(addr) {
			t.Errorf("expected %s to be allowed", raw)
		}
	}
}

func TestValidatePublicMediaURL(t *testing.T) {
	rejected := []string{
		"ftp://cdn.example.com/file.jpg",
		"file:///etc/passwd",
		"https://user:pass@cdn.example.com/file.jpg",
		"https://cdn.example.com/file.jpg#fragment",
		"https://cdn.example.com:8443/file.jpg",
		"http://cdn.example.com:8080/file.jpg",
		"https://cdn.example.com:80/file.jpg",
		"http://cdn.example.com:443/file.jpg",
		"https://localhost/file.jpg",
		"https://api.localhost/file.jpg",
		"https://printer.local/file.jpg",
		"https://metadata.google.internal/computeMetadata/v1/",
		"https://intranet/file.jpg",
		"https://127.0.0.1/file.jpg",
		"https://169.254.169.254/latest/meta-data/",
		"https://[::1]/file.jpg",
		"https://[64:ff9b::10.0.0.1]/file.jpg",
		"https://exämple.com/file.jpg",
	}
	for _, raw := range rejected {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if _, _, err := validatePublicMediaURL(u); !errors.Is(err, ErrPublicMediaBlocked) {
			t.Errorf("expected %q to be blocked, got %v", raw, err)
		}
	}

	accepted := []struct {
		raw  string
		host string
		port string
	}{
		{"https://cdn.example.com/media/1.jpg", "cdn.example.com", "443"},
		{"https://CDN.Example.COM:443/media/1.jpg", "cdn.example.com", "443"},
		{"http://cdn.example.com/media/1.jpg", "cdn.example.com", "80"},
		{"http://cdn.example.com:80/media/1.jpg", "cdn.example.com", "80"},
		{"https://93.184.216.34/media/1.jpg", "93.184.216.34", "443"},
	}
	for _, tc := range accepted {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		host, port, err := validatePublicMediaURL(u)
		if err != nil || host != tc.host || port != tc.port {
			t.Errorf("validate(%q) = (%q, %q, %v), want (%q, %q, nil)", tc.raw, host, port, err, tc.host, tc.port)
		}
	}
}

func TestPublicMediaTransportPinningRedirectsAndLimits(t *testing.T) {
	var seenHeaders []http.Header
	var seenSNI string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			seenSNI = r.TLS.ServerName
		}
		seenHeaders = append(seenHeaders, r.Header.Clone())
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("public-bytes"))
		case "/large":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("1234567890"))
		case "/redir-1":
			http.Redirect(w, r, "https://cdn.example.com/redir-2", http.StatusFound)
		case "/redir-2":
			http.Redirect(w, r, "https://cdn.example.com/redir-3", http.StatusFound)
		case "/redir-3":
			http.Redirect(w, r, "https://cdn.example.com/ok", http.StatusFound)
		case "/redir-4":
			http.Redirect(w, r, "https://cdn.example.com/redir-1", http.StatusFound)
		case "/redir-private":
			http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("secret-404-body"))
		case "/pending":
			w.WriteHeader(http.StatusAccepted)
		case "/server-error":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("secret-500-body"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	srvAddr := srv.Listener.Addr().String()
	var dialedAddrs []string
	newClient := func(ips []netip.Addr) *Client {
		client := NewClient("secret-line-token")
		client.PublicMediaClient = &http.Client{
			Transport: &publicMediaTransport{
				resolveIPs: func(context.Context, string) ([]netip.Addr, error) {
					return ips, nil
				},
				dialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					dialedAddrs = append(dialedAddrs, addr)
					return (&net.Dialer{}).DialContext(ctx, network, srvAddr)
				},
				tlsConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		return client
	}

	publicIP := netip.MustParseAddr("93.184.216.34")
	client := newClient([]netip.Addr{publicIP})

	// 1. Direct download pins resolved public IP, sets SNI, sends no LINE auth headers.
	data, err := client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/ok", OBSDownloadOptions{MaxBytes: 1024})
	if err != nil || string(data) != "public-bytes" {
		t.Fatalf("direct download failed: data=%q err=%v", data, err)
	}
	if seenSNI != "cdn.example.com" {
		t.Fatalf("SNI = %q, want cdn.example.com", seenSNI)
	}
	if len(dialedAddrs) == 0 || dialedAddrs[0] != "93.184.216.34:443" {
		t.Fatalf("dialed = %v, want pinned 93.184.216.34:443", dialedAddrs)
	}
	for _, h := range seenHeaders {
		for _, forbidden := range []string{"X-Line-Access", "Cookie", "X-Line-Application", "X-Talk-Meta", "Authorization"} {
			if h.Get(forbidden) != "" {
				t.Fatalf("forbidden header %s sent to external host", forbidden)
			}
		}
	}

	// 2. 3 redirects succeed, 4 redirects fail with ErrPublicMediaRedirectLimit.
	data, err = client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/redir-1", OBSDownloadOptions{MaxBytes: 1024})
	if err != nil || string(data) != "public-bytes" {
		t.Fatalf("3-hop redirect failed: data=%q err=%v", data, err)
	}
	_, err = client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/redir-4", OBSDownloadOptions{MaxBytes: 1024})
	if !errors.Is(err, ErrPublicMediaRedirectLimit) {
		t.Fatalf("4-hop redirect err = %v, want ErrPublicMediaRedirectLimit", err)
	}

	// 3. Redirect to private IP is blocked.
	_, err = client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/redir-private", OBSDownloadOptions{MaxBytes: 1024})
	if !errors.Is(err, ErrPublicMediaBlocked) {
		t.Fatalf("private redirect err = %v, want ErrPublicMediaBlocked", err)
	}

	// 4. Size limit enforced.
	_, err = client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/large", OBSDownloadOptions{MaxBytes: 5})
	if !errors.Is(err, ErrOBSSizeLimit) {
		t.Fatalf("oversized download err = %v, want ErrOBSSizeLimit", err)
	}

	// 5. Status classification never leaks raw response bodies.
	if _, err := client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/missing", OBSDownloadOptions{}); !errors.Is(err, ErrOBSObjectNotFound) {
		t.Fatalf("404 err = %v, want ErrOBSObjectNotFound", err)
	}
	if _, err := client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/pending", OBSDownloadOptions{}); !errors.Is(err, ErrOBSEncodingIncomplete) {
		t.Fatalf("202 err = %v, want ErrOBSEncodingIncomplete", err)
	}
	if _, err := client.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/server-error", OBSDownloadOptions{}); err == nil || strings.Contains(err.Error(), "secret-500-body") {
		t.Fatalf("500 error missing or leaked body: %v", err)
	}

	// 6. Mixed DNS response containing any private IP fails closed before dialing.
	beforeDials := len(dialedAddrs)
	mixedClient := newClient([]netip.Addr{publicIP, netip.MustParseAddr("10.0.0.1")})
	if _, err := mixedClient.DownloadOBSPublicResourceWithOptions(context.Background(), "https://cdn.example.com/ok", OBSDownloadOptions{}); !errors.Is(err, ErrPublicMediaBlocked) || len(dialedAddrs) != beforeDials {
		t.Fatalf("mixed private DNS not blocked before dial: err=%v dials=%d->%d", err, beforeDials, len(dialedAddrs))
	}
}
