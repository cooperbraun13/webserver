package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	BaseDir  string
	Port     int
	Threads  int
	Buffers  int
	SchedAlg string
}

type Request struct {
	Conn     net.Conn
	URI      string
	FullPath string
}

func parseRequest(conn net.Conn, baseDir string) (*Request, error) {
	reader := bufio.NewReader(conn)

	// request line: "GET /path HTTP/1.0"
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read request line: %w", err)
	}

	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 3 {
		return nil, fmt.Errorf("malformed request line: %q", line)
	}

	method, uri := parts[0], parts[1]
	if method != "GET" {
		return nil, fmt.Errorf("unsupported method: %s", method)
	}

	// simple header skip for HTTP/1.0/1.1
	for {
		hline, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed reading headers: %w", err)
		}
		if hline == "\r\n" || hline == "\n" {
			break
		}
	}

	if strings.Contains(uri, "..") {
		return nil, fmt.Errorf("path traversal attempt")
	}

	if uri == "/" {
		uri = "/index.html"
	}

	full := filepath.Join(baseDir, filepath.Clean(uri))

	return &Request{
		Conn:     conn,
		URI:      uri,
		FullPath: full,
	}, nil
}

func writeError(conn net.Conn, status int, message string) {

}

func httpStatusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 400:
		return "Bad Request"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 500:
		return "Internal Server Error"
	default:
		return "Unkown"
	}
}

func handleRequest(req *Request) {

}

func worker(id int, queue <-chan *Request) {

}

func listenAndServe(cfg Config) error {

}

func main() {

}
