package main

import (
	"bufio"         // buffered I/O for reading HTTP requests
	"flag"			// command-line argument parsing
	"fmt"           // formatted I/O (printing)
	"io"            // basic I/O interfaces
	"log"           // logging errors
	"mime"          // MIME type detection for files
	"net"           // network connections (TCP)
	"os"            // OS operations (file system)
	"path/filepath" // file path manipulation
	"strings"       // string operations
	"sync"          // synchronization primitives (mutexes, condition variables)
	"time"          // adding artificial delays
)

// Config holds all server settings that come from command-line flags
type Config struct {
	BaseDir  string // root directory where files are served from (e.g., "./www")
	Port     int    // TCP port to listen on (e.g., 8000)
	Threads  int    // number of worker threads in the pool
	Buffers  int    // maximum size of the request buffer/queue
	SchedAlg string // scheduling algorithm: FCFS or SFF
}

// Each Request represents one HTTP GET request waiting to be processed by a worker
type Request struct {
	Conn     net.Conn // the TCP connection to the clients browser
	URI      string   // the requested path (e.g., "/big.html")
	FullPath string   // full filesystem path (e.g., "./www/big.html")
	Size     int64    // file size in bytes (used for SFF scheduling)
}

// Scheduler manages a bounded buffer for producer-consumer request handling with 
// mutex-protected access and condition variables for efficient thread synchronization
type Scheduler struct {
	mu 		 sync.Mutex // mutex protects the buffer from race conditions
	notEmpty *sync.Cond // condition variable: signals workers when requests arrive
	notFull  *sync.Cond // condition variable: signals master when space opens up
	buf      []*Request // the bounded buffer (queue) of pending requests
	capacity int        // maximum number of requests that can be queued
	schedAlg string     // "FCFS" or "SFF" - determines which request to process next
}

// ---------------- scheduler ----------------

func NewScheduler(capacity int, schedAlg string) *Scheduler {
	s := &Scheduler {
		buf: make([]*Request, 0, capacity), // start with empty slice, max capacity
		capacity: capacity,
		schedAlg: schedAlg,
	}
	// both condition variables share the same mutex
	s.notEmpty = sync.NewCond(&s.mu)
	s.notFull = sync.NewCond(&s.mu)
	return s
}

// producer: master thread adds requests
func (s *Scheduler) Enqueue(req *Request) {
	// lock: get exclusive access to buffer
	s.mu.Lock()
	// unlock: release lock when function exits
	defer s.mu.Unlock()

	// blocking: wait while buffer is full
	for len(s.buf) >= s.capacity {
		// releases mutex, sleeps, reacquires when woken (prevents deadlock)
		s.notFull.Wait()
	}

	// add: buffer has space, add the request
	s.buf = append(s.buf, req)

	// signal: wake up one sleeping worker thread
	s.notEmpty.Signal()
}

// consumer: workers take requests
func (s *Scheduler) Dequeue() *Request {
	// lock: get exclusive access
	s.mu.Lock()
	// unlock: release when done
	defer s.mu.Unlock()

	// blocking: wait while buffer is empty
	for len(s.buf) == 0 {
		// releases mutex, sleeps, reacquires when woken
		s.notEmpty.Wait()
	}

	// scheduling decision: which request should be processed
	var idx int
	if s.schedAlg == "SFF" {
		// SFF: scan buffer for smallest file
		idx = 0
		minSize := s.buf[0].Size
		for i:= 1; i < len(s.buf); i++ {
			if s.buf[i].Size < minSize {
				minSize = s.buf[i].Size
				idx = i
			}
		}
	} else {
		// FCFS: just take first in line
		idx = 0
	}

	// remove: extract the chosen request
	req := s.buf[idx]
	// remove element at idx
	s.buf = append(s.buf[:idx], s.buf[idx+1:]...)

	// signal: tell master thread theres space now
	s.notFull.Signal()

	return req
}

// ---------------- HTTP parsing / responses ----------------

func parseRequest(conn net.Conn, baseDir string) (*Request, error) {
	reader := bufio.NewReader(conn)

	// read first line: "GET /path HTTP/1.0"
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read request line: %w", err)
	}

	// split into [method, URI, version]
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 3 {
		return nil, fmt.Errorf("malformed request line: %q", line)
	}

	method, rawURI := parts[0], parts[1]

	// only support GET for now
	if method != "GET" {
		return nil, fmt.Errorf("unsupported method: %s", method)
	}

	// skip HTTP headers because we dont need them
	for {
		hline, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed reading headers: %w", err)
		}
		// empty line = end of headers
		if hline == "\r\n" || hline == "\n" {
			break
		}
	}

	uri := rawURI

	// default "/" to index.html
	if uri == "/" {
		uri = "/index.html"
	}

	// convert URI to filesystem path
	// remove leading "/" so filepath.Join doesnt treat it as absolute
	uriPath := strings.TrimPrefix(uri, "/")

	// clean the path (removes ./, resolves .., etc.)
	clean := filepath.Clean(uriPath)

	// security: block directory traversal attacks
	if strings.Contains(clean, "..") {
		return nil, fmt.Errorf("path traversal attempt")
	}

	// build full path
	// example:
	// browser requests: GET /big.html HTTP/1.0
	// becomes: ./www/big.html
	full := filepath.Join(baseDir, clean)

	return &Request{
		Conn:     conn,
		URI:      uri,
		FullPath: full,
	}, nil
}

func writeError(conn net.Conn, status int, message string) {
	// create simple HTML error page
	body := fmt.Sprintf("<html><body><h1>%d %s</h1></body></html>", status, message)

	// send HTTP response line
	fmt.Fprintf(conn, "HTTP/1.0 %d %s\r\n", status, httpStatusText(status))

	// send headers
	fmt.Fprintf(conn, "Content-Type: text/html\r\n")
	fmt.Fprintf(conn, "Content-Length: %d\r\n", len(body))
	fmt.Fprintf(conn, "\r\n") // empty line ends headers

	// send body
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
	// always close connection when done
	defer req.Conn.Close()

	// try to open the requested file
	f, err := os.Open(req.FullPath)
	if err != nil {
		log.Printf("open error for %s: %v", req.FullPath, err)
		writeError(req.Conn, 404, "Not Found")
		return
	}
	defer f.Close()

	// get the file size
	var size int64
	if req.Size > 0 {
		// already got size during enqueueing (for SFF)
		size = req.Size
	} else {
		// need to stat the file
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			writeError(req.Conn, 404, "Not Found")
			return
		}
		size = info.Size()
	}

	// artificial delay: simulates disk I/O / processing time
	// makes scheduling diffferences observable
	// small.html (205 bytes): 205 / 10000 = 0.02ms
	// big.html (594129 bytes): 594129 / 10000 = 59.41 ms
	time.Sleep(time.Duration(size/10000) * time.Millisecond)

	// determine MIME type from file extension
	ct := mime.TypeByExtension(filepath.Ext(req.FullPath))
	if ct == "" {
		// generic binary
		ct = "application/octet-stream"
	}

	// send HTTP response headers
	fmt.Fprintf(req.Conn, "HTTP/1.0 200 OK\r\n")
	fmt.Fprintf(req.Conn, "Content-Type: %s\r\n", ct)
	fmt.Fprintf(req.Conn, "Content-Length: %d\r\n", size)
	fmt.Fprintf(req.Conn, "\r\n") // empty line ends headers

	// send file contents
	if _, err := io.Copy(req.Conn, f); err != nil {
		log.Printf("error sending file %s: %v", req.FullPath, err)
	}

}

// ---------------- workers & server ----------------

func worker(id int, sched *Scheduler) {
	for {
		req := sched.Dequeue() // block until work available
		log.Printf("worker %d handling %s", id, req.URI)
		handleRequest(req)     // process the request
	}
}

func listenAndServe(cfg Config) error {
	// create listening socket
	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen error: %w", err)
	}
	defer ln.Close()

	log.Printf("listening on %s (threads=%d, buffers=%d, sched=%s, basedir=%s)", addr, cfg.Threads, cfg.Buffers, cfg.SchedAlg, cfg.BaseDir)

	// create scheduler
	sched := NewScheduler(cfg.Buffers, cfg.SchedAlg)

	// start worker thread pool
	for i := 0; i < cfg.Threads; i++ {
		go worker(i, sched)
	}

	// main accept loop (master thread)
	for {
		// wait for a new connection
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}

		// handle each connection in its own goroutine
		// this goroutine parses and enqueues, then exits
		// it does not handle the request, workers do that
		go func(c net.Conn) {
			// parse HTTP request
			req, err := parseRequest(c, cfg.BaseDir)
			if err != nil {
				log.Printf("bad request: %v", err)
				writeError(c, 400, "Bad Request")
				c.Close()
				return
			}

			// get file size (needed for SFF scheduling)
			info, err := os.Stat(req.FullPath)
			if err != nil || info.IsDir() {
				log.Printf("stat/open error for %s: %v", req.FullPath, err)
				writeError(c, 404, "Not Found")
				c.Close()
				return
			}
			req.Size = info.Size()

			// add to queue (blocks if buffer is full)
			sched.Enqueue(req)
		}(conn)
	}
}

// ---------------- main ----------------

func main() {
	// define command line flags
	var cfg Config
	flag.StringVar(&cfg.BaseDir, "d", "./www", "base directory")
	flag.IntVar(&cfg.Port, "p", 10000, "port")
	flag.IntVar(&cfg.Threads, "t", 1, "worker threads")
	flag.IntVar(&cfg.Buffers, "b", 1, "buffer size")
	flag.StringVar(&cfg.SchedAlg, "s", "FCFS", "scheduling algorithm (FCFS or SFF)")
	flag.Parse()

	// validate scheduling algorithm
	cfg.SchedAlg = strings.ToUpper(cfg.SchedAlg)
	if cfg.SchedAlg != "FCFS" && cfg.SchedAlg != "SFF" {
		log.Fatalf("unsupported schedule algorithm %q (must be FCFS or SFF)", cfg.SchedAlg)
	}

	// validate valid thread and buffer counts
	if cfg.Threads <= 0 || cfg.Buffers <= 0 {
		log.Fatalf("threads and buffers must be positive")
	}

	// start the server
	if err := listenAndServe(cfg); err != nil {
		log.Fatal(err)
	}
}
