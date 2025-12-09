package main

import (
	"bufio" // buffered reading of HTTP responses
	"fmt"   // formatted output
	"log"   // error logging
	"net"   // network connections (TCP)
	"sync"  // WaitGroup and Mutex for synchronization
	"time"  // timing measurements and delays
)

type Results struct {
	mu 		   sync.Mutex      // protects the slices from concurrent access
	smallTimes []time.Duration // response times for small.html requests
	bigTimes   []time.Duration // response times for big.html requests
}

func sendRequest(path string, wg *sync.WaitGroup, results *Results) {
	// tell WaitGroup were done when the function exits
	defer wg.Done()
	
	// start timer: measure total response time
	start := time.Now()

	// connect: open tcp connection to server
	conn, err := net.Dial("tcp", "localhost:8000")
	if err != nil {
		log.Println("dial error:", err)
		return
	}
	// close connection when done
	defer conn.Close()

	// send HTTP request: simple GET request
	fmt.Fprintf(conn, "GET %s HTTP/1.0\r\nHost: localhost\r\n\r\n", path)

	// read response: read entire response (header + body)
	reader := bufio.NewReader(conn)
	for {
		_, err := reader.ReadString('\n')
		if err != nil {
			break // EOF or error = response complete
		}
	}

	// stop timer: calculate total time
	duration := time.Since(start)
	fmt.Printf("%s completed in %v\n", path, duration)

	// store results: use mutex to safely append to shared slice
	results.mu.Lock()
	if path == "/small.html" {
		results.smallTimes = append(results.smallTimes, duration)
	} else {
		results.bigTimes = append(results.bigTimes, duration)
	}
	results.mu.Unlock()
}

func main() {
	// setup: create WaitGroup and Results
	var wg sync.WaitGroup
	results := &Results{
		smallTimes: make([]time.Duration, 0),
		bigTimes: make([]time.Duration, 0),
	}

	fmt.Println("sending burst of requests...")

	// phase 1: launch 5 big file requests
	for i := 0; i < 5; i++ {
		// tell WaitGroup to expect on more goroutine
		wg.Add(1)
		go sendRequest("/big.html", &wg, results)
	}

	// give big files time to reach the server first
	time.Sleep(50 * time.Millisecond)

	// phase 2: launch 10 small file requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go sendRequest("/small.html", &wg, results)
	}

	// wait: block until all goroutines finish
	wg.Wait()

	// calculate statistics
	fmt.Printf("\n------------ Results ------------\n")
	if len(results.smallTimes) > 0 {
		avg := average(results.smallTimes)
		fmt.Printf("small files: avg=%v, count=%d\n", avg, len(results.smallTimes))
	} else {
		fmt.Println("small files: no responses received")
	}
	if len(results.bigTimes) > 0 {
		avg := average(results.bigTimes)
		fmt.Printf("big files: avg=%v, count=%d\n", avg, len(results.bigTimes))
	} else {
		fmt.Println("big files: no responses received")
	}
}

func average(times []time.Duration) time.Duration {
	var sum time.Duration
	for _, t := range times {
		sum += t
	}
	return sum / time.Duration(len(times))
}
