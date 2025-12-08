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
	"sync"
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
	Size     int64 // used for SJF (or SFF, shortest file first in my case) scheduling
}

type Scheduler struct {
	mu 		 sync.Mutex
	notEmpty *sync.Cond
	notFull  *sync.Cond
	buf      []*Request
	capacity int
	schedAlg string // "FCFS" or "SFF"
}

// ---------------- scheduler ----------------

func NewScheduler(capacity int, schedAlg string) *Scheduler {
	s := &Scheduler {
		buf: make([]*Request, 0, capacity),
		capacity: capacity,
		schedAlg: schedAlg,
	}
	s.notEmpty = sync.NewCond(&s.mu)
	s.notFull = sync.NewCond(&s.mu)
	return s
}

func (s *Scheduler) Enqueue(req *Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for len(s.buf) >= s.capacity {
		s.notFull.Wait()
	}
	s.buf = append(s.buf, req)
	s.notEmpty.Signal()
}

func (s *Scheduler) Dequeue() *Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	for len(s.buf) == 0 {
		s.notEmpty.Wait()
	}

	var idx int
	if s.schedAlg == "SFF" {
		idx = 0
		minSize := s.buf[0].Size
		for i:= 1; i < len(s.buf); i++ {
			if s.buf[i].Size < minSize {
				minSize = s.buf[i].Size
				idx = i
			}
		}
	} else {
		idx = 0 // FCFS
	}

	req := s.buf[idx]
	s.buf = append(s.buf[:idx], s.buf[idx+1:]...)
	s.notFull.Signal()
	return req
}

// ---------------- HTTP parsing / responses ----------------

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

	method, rawURI := parts[0], parts[1]
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

	uri := rawURI

	if uri == "/" {
		uri = "/index.html"
	}

	// strip leading "/" so filepath.Join doesnt treat it as absolute
	uriPath := strings.TrimPrefix(uri, "/")

	// clean the path
	clean := filepath.Clean(uriPath)

	if strings.Contains(clean, "..") {
		return nil, fmt.Errorf("path traversal attempt")
	}

	full := filepath.Join(baseDir, clean)

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
	fmt.Fprintf(conn, "%s", body)
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
		return "Unknown"
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

	var size int64
	if req.Size > 0 {
		size = req.Size
	} else {
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			writeError(req.Conn, 404, "Not Found")
			return
		}
		size = info.Size()
	}

	ct := mime.TypeByExtension(filepath.Ext(req.FullPath))
	if ct == "" {
		ct = "application/octet-stream"
	}

	// headers
	fmt.Fprintf(req.Conn, "HTTP/1.0 200 OK\r\n")
	fmt.Fprintf(req.Conn, "Content-Type: %s\r\n", ct)
	fmt.Fprintf(req.Conn, "Content-Length: %d\r\n", size)
	fmt.Fprintf(req.Conn, "\r\n")

	// body
	if _, err := io.Copy(req.Conn, f); err != nil {
		log.Printf("error sending file %s: %v", req.FullPath, err)
	}

}

// ---------------- workers & server ----------------

func worker(id int, sched *Scheduler) {
	for {
		req := sched.Dequeue()
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

	sched := NewScheduler(cfg.Buffers, cfg.SchedAlg)

	// start worker goroutines
	for i := 0; i < cfg.Threads; i++ {
		go worker(i, sched)
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
			info, err := os.Stat(req.FullPath)
			if err != nil || info.IsDir() {
				log.Printf("stat/open error for %s: %v", req.FullPath, err)
				writeError(c, 404, "Not Found")
				c.Close()
				return
			}
			req.Size = info.Size()

			sched.Enqueue(req)
		}(conn)
	}
}

// ---------------- main ----------------

func main() {
	var cfg Config
	flag.StringVar(&cfg.BaseDir, "d", "./www", "base directory")
	flag.IntVar(&cfg.Port, "p", 10000, "port")
	flag.IntVar(&cfg.Threads, "t", 1, "worker threads")
	flag.IntVar(&cfg.Buffers, "b", 1, "buffer size")
	flag.StringVar(&cfg.SchedAlg, "s", "FCFS", "scheduling algorithm (FCFS or SFF)")
	flag.Parse()

	cfg.SchedAlg = strings.ToUpper(cfg.SchedAlg)
	if cfg.SchedAlg != "FCFS" && cfg.SchedAlg != "SFF" {
		log.Fatalf("unsupported schedule algorithm %q (must be FCFS or SFF)", cfg.SchedAlg)
	}
	if cfg.Threads <= 0 || cfg.Buffers <= 0 {
		log.Fatalf("threads and buffers must be positive")
	}

	if err := listenAndServe(cfg); err != nil {
		log.Fatal(err)
	}
}
