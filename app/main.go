package main

import (
	"fmt"
	"net"
	"os"
)

// Ensures gofmt doesn't remove the "net" and "os" imports above (feel free to remove this!)
var _ = net.Listen
var _ = os.Exit

func main() {
	fmt.Println("Server listening on Port 4221")

	ln, err := net.Listen("tcp", "127.0.0.1:4221")

	if err != nil {
		fmt.Println("Error starting server on port: " + err.Error())
		os.Exit(1)
	}

	_, err = ln.Accept()
	if err != nil {
		fmt.Println("Error accepting connection from remote: " + err.Error())
		os.Exit(1)
	}

}
