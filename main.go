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
	body := fmt.Sprintf("<html><body><h1>%d %s</h1></body></html>", status, message)
	fmt.Fprintf(conn, "HTTP/1.0 %d %s\r\n", status, httpStatusText(status))
	fmt.Fprintf(conn, "Content-Type: text/html\r\n")
	fmt.Fprintf(conn, "Content-Length: %d\r\n", len(body))
	fmt.Fprintf(conn, "\r\n")
	fmt.Fprintf(conn, body)
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
	defer req.Conn.Close()

	f, err := os.Open(req.FullPath)
	if err != nil {
		log.Printf("open error for %s: %v", req.FullPath, err)
		writeError(req.Conn, 404, "Not Found")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(req.Conn, 404, "Not Found")
		return
	}

	ct := mime.TypeByExtension(filepath.Ext(req.FullPath))
	if ct == "" {
		ct = "application/octet-stream"
	}

	// headers
	fmt.Fprintf(req.Conn, "HTTP/1.0 200 OK \r\n")
	fmt.Fprintf(req.Conn, "Content-Type: %s\r\n", ct)
	fmt.Fprintf(req.Conn, "Content-Length: %d\r\n", info.Size())
	fmt.Fprintf(req.Conn, "\r\n")

	// body
	if _, err := io.Copy(req.Conn, f); err != nil {
		log.Printf("error sending file %s: %v", req.FullPath, err)
	}

}

func worker(id int, queue <-chan *Request) {
	for req := range queue {
		handleRequest(req)
	}
}

func listenAndServe(cfg Config) error {
	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen error: %w", err)
	}
	defer ln.Close()

	log.Printf("listening on %s (threads=%d, buffers=%d, sched=%s, basedir=%s)", addr, cfg.Threads, cfg.Buffers, cfg.SchedAlg, cfg.BaseDir)

	// FCFS bounded queue
	queue := make(chan *Request, cfg.Buffers)

	// start worker goroutines
	for i := 0; i < cfg.Threads; i++ {
		go worker(i, queue)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}

		go func(c net.Conn) {
			req, err := parseRequest(c, cfg.BaseDir)
			if err != nil {
				log.Printf("bad request: %v", err)
				writeError(c, 400, "Bad Request")
				c.Close()
				return
			}

			// blocks when buffer is full = bounded buffer
			queue <- req
		}(conn)
	}
}

func main() {
	var cfg Config
	flag.StringVar(&cfg.BaseDir, "d", "./www", "base directory")
	flag.IntVar(&cfg.Port, "p", 10000, "port")
	flag.IntVar(&cfg.Threads, "t", 1, "worker threads")
	flag.IntVar(&cfg.Buffers, "b", 1, "buffer size")
	flag.StringVar(&cfg.SchedAlg, "s", "FCFS", "scheduling algorithm (only FCFS for now)")
	flag.Parse()

	cfg.SchedAlg = strings.ToUpper(cfg.SchedAlg)

	if err := listenAndServe(cfg); err != nil {
		log.Fatal(err)
	}
}
