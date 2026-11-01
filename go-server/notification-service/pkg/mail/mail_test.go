package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts one connection, speaks just enough SMTP, and sends the
// received DATA body on the returned channel.
func fakeSMTP(t *testing.T) (addr string, data <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	out := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { conn.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		var body strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case inData && line == ".":
				inData = false
				out <- body.String()
				say("250 queued")
			case inData:
				body.WriteString(line + "\n")
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				say("250 fake")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				say("354 go ahead")
			case strings.HasPrefix(line, "QUIT"):
				say("221 bye")
				return
			default: // MAIL, RCPT, RSET
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), out
}

func TestSendDeliversMessage(t *testing.T) {
	addr, data := fakeSMTP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := Send(ctx, addr, nil, "from@example.com", []string{"to@example.com"}, []byte("Subject: hi\r\n\r\nhello"))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case got := <-data:
		if !strings.Contains(got, "hello") {
			t.Fatalf("server received %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive a message")
	}
}

func TestSendHonoursContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// A listener that accepts but never speaks forces Send to block on the greeting.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer silent.Close()

	timedOut, cancelTimeout := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelTimeout()

	tests := []struct {
		name string
		ctx  context.Context
		addr string
		want error
	}{
		{"already cancelled", cancelled, "127.0.0.1:1", context.Canceled},
		{"cancelled while waiting for greeting", timedOut, silent.Addr().String(), context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- Send(tt.ctx, tt.addr, nil, "a@b.c", []string{"d@e.f"}, []byte("x")) }()
			select {
			case err := <-done:
				if !errors.Is(err, tt.want) {
					t.Fatalf("got %v, want %v", err, tt.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Send did not return after context ended")
			}
		})
	}
}

func TestSendRejectsBadAddress(t *testing.T) {
	if err := Send(context.Background(), "no-port", nil, "a@b.c", nil, nil); err == nil {
		t.Fatal("expected error for address without port")
	}
}
