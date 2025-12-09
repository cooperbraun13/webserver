package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type Results struct {
	mu 		   sync.Mutex
	smallTimes []time.Duration
	bigTimes   []time.Duration
}

func sendRequest(path string, wg *sync.WaitGroup, results *Results) {
	defer wg.Done()
	
	start := time.Now()
	conn, err := net.Dial("tcp", "localhost:8000")
	if err != nil {
		log.Println("dial error:", err)
		return
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET %s HTTP/1.0\r\nHost: localhost\r\n\r\n", path)

	// read full response
	reader := bufio.NewReader(conn)
	for {
		_, err := reader.ReadString('\n')
		if err != nil {
			break
		}
	}

	duration := time.Since(start)
	fmt.Printf("%s completed in %v\n", path, duration)

	// store results with mutex protection
	results.mu.Lock()
	if path == "/small.html" {
		results.smallTimes = append(results.smallTimes, duration)
	} else {
		results.bigTimes = append(results.bigTimes, duration)
	}
	results.mu.Unlock()
}

func main() {
	var wg sync.WaitGroup
	results := &Results{
		smallTimes: make([]time.Duration, 0),
		bigTimes: make([]time.Duration, 0),
	}

	fmt.Println("sending burst of requests...")

	// first, send several big file requests
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go sendRequest("/big.html", &wg, results)
	}

	// small delay to ensure big files enter queue first
	time.Sleep(50 * time.Millisecond)

	// now send small file requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go sendRequest("/small.html", &wg, results)
	}

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
