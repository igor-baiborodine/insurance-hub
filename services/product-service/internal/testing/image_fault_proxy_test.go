//go:build image

package integrationtest

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Only synthetic, owned PostgreSQL traffic crosses this test proxy. Terminating TLS
// lets the test drop a server acknowledgement after PostgreSQL has executed COMMIT
// (or an INSERT), without adding fault switches to the production command.
type imageFaultProxy struct {
	port       string
	fired      atomic.Bool
	command    string
	occurrence int64
	seen       atomic.Int64
}

func newImageFaultProxy(
	t *testing.T,
	backend string,
	files imageTLSFiles,
	command string,
	occurrence int64,
) *imageFaultProxy {
	t.Helper()
	certificate, err := tls.LoadX509KeyPair(files.cert, files.key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &imageFaultProxy{
		port:       strconv.Itoa(listener.Addr().(*net.TCPAddr).Port),
		command:    command,
		occurrence: occurrence,
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { _ = client.Close() }()
				stop := context.AfterFunc(ctx, func() { _ = client.Close() })
				defer stop()
				_ = client.SetDeadline(time.Now().Add(2 * time.Minute))
				request := make([]byte, 8)
				if _, err := io.ReadFull(client, request); err != nil {
					return
				}
				if binary.BigEndian.Uint32(request[:4]) != 8 ||
					binary.BigEndian.Uint32(request[4:]) != 80877103 {
					return
				}
				if _, err := client.Write([]byte("S")); err != nil {
					return
				}
				secure := tls.Server(
					client,
					&tls.Config{
						Certificates: []tls.Certificate{certificate},
						MinVersion:   tls.VersionTLS12,
					},
				)
				if err := secure.HandshakeContext(ctx); err != nil {
					return
				}
				upstream, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(
					ctx,
					"tcp",
					backend,
				)
				if err != nil {
					return
				}
				defer func() { _ = upstream.Close() }()
				stopUpstream := context.AfterFunc(
					ctx,
					func() { _ = upstream.Close() },
				)
				defer stopUpstream()
				_ = upstream.SetDeadline(time.Now().Add(2 * time.Minute))
				copied := make(chan struct{})
				go func() { defer close(copied); _, _ = io.Copy(upstream, secure); _ = upstream.Close() }()
				defer func() { _ = client.Close(); _ = upstream.Close(); <-copied }()
				for {
					header := make([]byte, 5)
					if _, err := io.ReadFull(upstream, header); err != nil {
						return
					}
					size := binary.BigEndian.Uint32(header[1:])
					if size < 4 || size > 8*1024*1024 {
						return
					}
					payload := make([]byte, size-4)
					if _, err := io.ReadFull(upstream, payload); err != nil {
						return
					}
					if header[0] == 'C' &&
						string(payload) == proxy.command+"\x00" &&
						proxy.seen.Add(1) == proxy.occurrence {
						proxy.fired.Store(true)
						return
					}
					if _, err := secure.Write(
						append(header, payload...),
					); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("fault proxy did not release its connections")
		}
	})
	return proxy
}
