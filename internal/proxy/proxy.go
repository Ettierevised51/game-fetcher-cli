// Package proxy implements the built-in local forward HTTP proxy that rate
// limits steamcmd downloads (spec.md section 9). Standard library only.
//
// All child steamcmd processes point http_proxy at one Server, so the shared
// token bucket caps the summary rate. HTTPS content is throttled by counting
// raw bytes inside CONNECT tunnels — no TLS interception.
//
// Verified live on steamcmd version 1782532820 (2026-07): with
// http_proxy=127.0.0.1:<port> (no scheme — steamcmd chokes on one) every
// byte of CDN traffic flows through the proxy (client config as plain-HTTP
// GET, depot content as CONNECT to *.steamcontent.com:443), the download
// succeeds, and a dead proxy stalls it at 0 bytes — steamcmd does not
// silently bypass the variable. Note the limiter meters network (compressed)
// bytes: ~37 MB on the wire produced ~108 MB on disk in the live test.
package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Server is a running proxy instance.
type Server struct {
	bucket      *bucket
	ln          net.Listener
	srv         *http.Server
	transferred atomic.Int64
}

// Listen starts the proxy on an ephemeral localhost port. bytesPerSec <= 0
// means counting only, no throttling.
func Listen(bytesPerSec int64) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{bucket: newBucket(bytesPerSec), ln: ln}
	s.srv = &http.Server{Handler: s}
	go s.srv.Serve(ln)
	return s, nil
}

// Addr returns "127.0.0.1:<port>" — exactly the value steamcmd expects in
// http_proxy (scheme-less; see the package comment).
func (s *Server) Addr() string {
	return s.ln.Addr().String()
}

// Transferred reports the total bytes moved through the proxy so far.
func (s *Server) Transferred() int64 {
	return s.transferred.Load()
}

func (s *Server) Close() error {
	return s.srv.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.tunnel(w, r)
		return
	}
	s.forward(w, r)
}

// tunnel serves CONNECT: a raw byte pipe, throttled in both directions.
// This is where steamcmd's depot traffic flows.
func (s *Server) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := net.DialTimeout("tcp", r.Host, 15*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		s.copyLimited(upstream, client) // uploads: requests, acks
		close(done)
	}()
	s.copyLimited(client, upstream) // downloads: the bytes that matter
	// The download side is finished; unblock the upload copy too instead of
	// waiting for a client that may keep its half open idle forever.
	client.Close()
	upstream.Close()
	<-done
}

// forward serves plain absolute-URI proxy requests (steamcmd's client
// config fetches).
func (s *Server) forward(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "this is a forward proxy", http.StatusBadRequest)
		return
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	outReq.Header.Del("Proxy-Connection")
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	s.copyLimited(w, resp.Body)
}

func (s *Server) copyLimited(dst io.Writer, src io.Reader) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			s.bucket.wait(n)
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			s.transferred.Add(int64(n))
			if f, ok := dst.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ParseRate parses a human rate like "500K", "10M", "1G" or a plain number
// into bytes per second (binary multiples).
func ParseRate(s string) (int64, error) {
	v := strings.TrimSpace(strings.ToUpper(s))
	if v == "" {
		return 0, fmt.Errorf("empty rate")
	}
	mult := int64(1)
	switch v[len(v)-1] {
	case 'K':
		mult, v = 1<<10, v[:len(v)-1]
	case 'M':
		mult, v = 1<<20, v[:len(v)-1]
	case 'G':
		mult, v = 1<<30, v[:len(v)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("rate %q: expected a positive number with an optional K/M/G suffix", s)
	}
	return n * mult, nil
}
