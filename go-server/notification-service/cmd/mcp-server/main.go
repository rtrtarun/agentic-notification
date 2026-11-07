// Command mcp-server speaks the Model Context Protocol over stdio, so Claude Desktop
// or Cursor can use AgentPulse. It reads one JSON-RPC message per line from stdin,
// writes one response per line to stdout, and logs only to stderr.
//
// Configuration, from the environment:
//
//	AGENTPULSE_URL      the service address, for example https://agentpulse.example.com
//	AGENTPULSE_API_KEY  the API key the tools act with
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/agentpulse/notification-service/pkg/mcp"
)

// maxLine bounds one incoming message.
const maxLine = 1 << 20

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	backend, err := mcp.NewHTTPBackend(os.Getenv("AGENTPULSE_URL"), os.Getenv("AGENTPULSE_API_KEY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-server:", err)
		os.Exit(2)
	}
	if err := serve(ctx, os.Stdin, os.Stdout, mcp.New(backend, version)); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-server:", err)
		os.Exit(1)
	}
}

// serve answers each line of in on out until in ends or ctx is cancelled.
func serve(ctx context.Context, in io.Reader, out io.Writer, s *mcp.Server) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if reply := s.Handle(ctx, line); reply != nil {
			if _, err := out.Write(append(reply, '\n')); err != nil {
				return fmt.Errorf("write response: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	return nil
}
