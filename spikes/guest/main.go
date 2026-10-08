// Spike guest program for S3: dials the host over vsock (CID 2) and runs a yamux session
// in which both sides open streams. Build with GOOS=linux CGO_ENABLED=0.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/mdlayher/vsock"
)

func main() {
	port := flag.Uint("port", 5000, "vsock port routed to the host socket")
	flag.Parse()
	c, err := vsock.Dial(2, uint32(*port), nil)
	if err != nil {
		log.Fatalf("dial vsock: %v", err)
	}
	sess, err := yamux.Client(c, nil)
	if err != nil {
		log.Fatal(err)
	}
	// Serve host-initiated streams (echo).
	go func() {
		for {
			st, err := sess.Accept()
			if err != nil {
				return
			}
			go func() {
				defer st.Close()
				line, _ := bufio.NewReader(st).ReadString('\n')
				fmt.Fprintf(st, "echo: %s\n", strings.TrimSpace(line))
				log.Printf("host->guest %q", strings.TrimSpace(line))
			}()
		}
	}()
	// Guest-initiated stream.
	st, err := sess.Open()
	if err != nil {
		log.Fatal(err)
	}
	t := time.Now()
	fmt.Fprintln(st, "hello host, from guest")
	line, _ := bufio.NewReader(st).ReadString('\n')
	log.Printf("guest->host reply %q in %s", strings.TrimSpace(line), time.Since(t))
	st.Close()
	time.Sleep(2 * time.Second)
	fmt.Println("S3 OK")
}
