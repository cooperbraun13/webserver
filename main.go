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

}

func writeError(conn net.Conn, status int, message string) {

}

func httpStatusText(code int) string {

}

func handleRequest(req *Request) {

}

func worker(id int, queue <-chan *Request) {

}

func listenAndServe(cfg Config) error {

}

func main() {

}
