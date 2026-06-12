/*
Copyright 2026 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// diag_tcpecho measures raw request/response RTT over TCP with
// length-prefixed frames, to establish the floor for a custom
// vtgate<->vttablet transport. Run as server in one container and
// client in another.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"time"
)

func main() {
	mode := flag.String("mode", "server", "server or client")
	addr := flag.String("addr", ":19999", "listen/connect address")
	size := flag.Int("size", 200, "request payload bytes (response is 4x)")
	n := flag.Int("n", 20000, "number of round trips")
	flag.Parse()

	if *mode == "server" {
		ln, err := net.Listen("tcp", *addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			go serve(c)
		}
	}

	c, err := net.Dial("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	c.(*net.TCPConn).SetNoDelay(true)

	req := make([]byte, 4+*size)
	binary.BigEndian.PutUint32(req, uint32(*size))
	respBuf := make([]byte, 4+4*(*size))

	roundTrip := func() {
		if _, err := c.Write(req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if _, err := io.ReadFull(c, respBuf[:4]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		rlen := binary.BigEndian.Uint32(respBuf[:4])
		if _, err := io.ReadFull(c, respBuf[4:4+rlen]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	for i := 0; i < 2000; i++ {
		roundTrip()
	}
	lat := make([]time.Duration, *n)
	start := time.Now()
	for i := 0; i < *n; i++ {
		qs := time.Now()
		roundTrip()
		lat[i] = time.Since(qs)
	}
	total := time.Since(start)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	fmt.Printf("n=%d mean=%.1fus p50=%.1fus p90=%.1fus p99=%.1fus\n",
		*n,
		float64(total.Microseconds())/float64(*n),
		float64(lat[*n/2].Nanoseconds())/1000,
		float64(lat[*n*9/10].Nanoseconds())/1000,
		float64(lat[*n*99/100].Nanoseconds())/1000)
}

func serve(c net.Conn) {
	defer c.Close()
	c.(*net.TCPConn).SetNoDelay(true)
	hdr := make([]byte, 4)
	buf := make([]byte, 1<<20)
	for {
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		rlen := binary.BigEndian.Uint32(hdr)
		if _, err := io.ReadFull(c, buf[:rlen]); err != nil {
			return
		}
		resp := make([]byte, 4+4*rlen)
		binary.BigEndian.PutUint32(resp, 4*rlen)
		if _, err := c.Write(resp); err != nil {
			return
		}
	}
}
