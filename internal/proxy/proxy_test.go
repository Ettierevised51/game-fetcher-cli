package proxy

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"500K", 500 << 10, true},
		{"10M", 10 << 20, true},
		{"1G", 1 << 30, true},
		{"123456", 123456, true},
		{" 2m ", 2 << 20, true},
		{"", 0, false},
		{"-5M", 0, false},
		{"fast", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseRate(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("ParseRate(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("ParseRate(%q) must fail", tc.in)
		}
	}
}

// TestBucketTiming drives the bucket with a fake clock: draining the burst
// and then pushing rate-sized chunks must sleep ~1s per chunk.
func TestBucketTiming(t *testing.T) {
	b := newBucket(1 << 20) // 1 MiB/s, burst 1 MiB
	now := time.Unix(0, 0)
	var slept time.Duration
	b.now = func() time.Time { return now }
	b.sleep = func(d time.Duration) { slept += d; now = now.Add(d) }
	b.tokens = float64(1 << 20)
	b.last = now

	b.wait(1 << 20) // consumes the whole burst — no sleep yet
	if slept != 0 {
		t.Fatalf("burst consumption slept %v", slept)
	}
	b.wait(1 << 20) // bucket empty: one full second to earn 1 MiB
	if slept < 900*time.Millisecond || slept > 1100*time.Millisecond {
		t.Fatalf("slept %v, want ~1s", slept)
	}
	b.wait(512 << 10) // half a second more
	if slept < 1400*time.Millisecond || slept > 1600*time.Millisecond {
		t.Fatalf("slept %v, want ~1.5s", slept)
	}
}

func TestBucketUnlimited(t *testing.T) {
	b := newBucket(0)
	b.sleep = func(time.Duration) { t.Fatal("unlimited bucket must never sleep") }
	b.wait(100 << 20)
}

func proxyClient(t *testing.T, s *Server) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse("http://" + s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
}

// TestForwardPlainHTTP covers the path steamcmd uses for client configs.
func TestForwardPlainHTTP(t *testing.T) {
	payload := strings.Repeat("x", 100<<10)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, payload)
	}))
	defer backend.Close()

	s, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resp, err := proxyClient(t, s).Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != payload {
		t.Fatalf("payload corrupted: got %d bytes", len(body))
	}
	if s.Transferred() < int64(len(payload)) {
		t.Fatalf("Transferred() = %d, want >= %d", s.Transferred(), len(payload))
	}
}

// TestConnectTunnel covers the CONNECT path steamcmd's depot downloads use.
func TestConnectTunnel(t *testing.T) {
	payload := strings.Repeat("y", 64<<10)
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, payload)
	}))
	defer backend.Close()

	s, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resp, err := proxyClient(t, s).Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != payload {
		t.Fatalf("payload corrupted through the tunnel: got %d bytes", len(body))
	}
	// TLS overhead means more raw bytes than payload.
	if s.Transferred() < int64(len(payload)) {
		t.Fatalf("Transferred() = %d, want >= %d", s.Transferred(), len(payload))
	}
}

// TestThrottling is a coarse wall-clock check: 256 KiB at 512 KiB/s with a
// drained burst must take a noticeable fraction of a second.
func TestThrottling(t *testing.T) {
	payload := strings.Repeat("z", 256<<10)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, payload)
	}))
	defer backend.Close()

	s, err := Listen(512 << 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.bucket.tokens = 0 // drain the initial burst for determinism

	start := time.Now()
	resp, err := proxyClient(t, s).Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("256KiB at 512KiB/s finished in %v — limiter not applied", elapsed)
	}
}
