package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func startTestServer(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	path := filepath.Join(dir, "simurghd.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	srv := NewServer(ln)
	go func() { errc <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errc:
		case <-time.After(2 * time.Second):
			t.Errorf("Serve did not exit")
		}
	})
	return path
}

func dialEncodeDecode(t *testing.T, socketPath string, req Request) Response {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := Encode(conn, req); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	resp, err := DecodeResponse(conn)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	return resp
}

func TestServerDaemonPing(t *testing.T) {
	path := startTestServer(t)

	resp := dialEncodeDecode(t, path, Request{
		V: ProtocolVersion, ID: "ping-1", Method: "daemon.ping",
	})
	if !resp.OK || resp.ID != "ping-1" || resp.Error != nil {
		t.Fatalf("response = %+v", resp)
	}
	var result struct {
		Pong bool `json:"pong"`
		PID  int  `json:"pid"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if !result.Pong {
		t.Fatalf("pong = false")
	}
	if result.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", result.PID, os.Getpid())
	}
}

func TestServerUnknownMethod(t *testing.T) {
	path := startTestServer(t)

	resp := dialEncodeDecode(t, path, Request{
		V: ProtocolVersion, ID: "u1", Method: "lease.acquire",
	})
	if resp.OK || resp.Error == nil {
		t.Fatalf("want error response, got %+v", resp)
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("code = %q, want %s", resp.Error.Code, CodeMethodNotFound)
	}
	if resp.Error.Fix != FixMethodNotFound {
		t.Fatalf("fix = %q, want %q", resp.Error.Fix, FixMethodNotFound)
	}
	if resp.Error.Message == "" {
		t.Fatal("empty message")
	}
}

func TestServerConcurrentClients(t *testing.T) {
	path := startTestServer(t)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"c1", "c2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			conn, err := net.DialTimeout("unix", path, time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			req := Request{V: ProtocolVersion, ID: id, Method: "daemon.ping"}
			if err := Encode(conn, req); err != nil {
				errs <- err
				return
			}
			resp, err := DecodeResponse(conn)
			if err != nil {
				errs <- err
				return
			}
			if !resp.OK || resp.ID != id {
				errs <- fmt.Errorf("id=%s resp=%+v", id, resp)
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestServerMalformedFrameDoesNotCrash(t *testing.T) {
	path := startTestServer(t)

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, _ = conn.Write([]byte("{not-json\n"))
	_ = conn.Close()

	// Give the server a moment to finish the bad client goroutine.
	time.Sleep(50 * time.Millisecond)

	resp := dialEncodeDecode(t, path, Request{
		V: ProtocolVersion, ID: "after-bad", Method: "daemon.ping",
	})
	if !resp.OK || resp.ID != "after-bad" {
		t.Fatalf("after malformed: %+v", resp)
	}
}
