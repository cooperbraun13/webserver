package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
)

func sendRequest(path string) {
	conn, err := net.Dial("tcp", "localhost:8000")
	if err != nil {
		log.Println("dial error:", err)
		return
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET %s HTTP/1.0\r\nHost: localhost\r\n\r\n", path)

	// read status line and then drop the rest
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err == nil {
		fmt.Printf("%s -> %s", path, line)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: client [N]")
		return
	}

	var wg sync.WaitGroup
	// spam small and big files interleaved
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sendRequest("/big.html")
		}()
		go func() {
			defer wg.Done()
			sendRequest("/small.html")
		}()
	}
	wg.Wait()
}
