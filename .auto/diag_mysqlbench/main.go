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

// diag_mysqlbench measures vitess go/mysql client latency for a single
// query in a tight loop, to compare against raw C client (mysqlslap)
// numbers on the same socket.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"vitess.io/vitess/go/mysql"
)

func main() {
	socket := flag.String("socket", "", "unix socket path")
	db := flag.String("db", "vt_main", "database name")
	query := flag.String("query", "select c from sbtest1 where id = 5077 limit 10001", "query to run")
	n := flag.Int("n", 20000, "number of queries")
	flag.Parse()

	params := &mysql.ConnParams{
		UnixSocket: *socket,
		Uname:      "root",
		DbName:     *db,
	}
	ctx := context.Background()
	conn, err := mysql.Connect(ctx, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Warmup.
	for i := 0; i < 1000; i++ {
		if _, err := conn.ExecuteFetch(*query, 10001, true); err != nil {
			fmt.Fprintf(os.Stderr, "exec: %v\n", err)
			os.Exit(1)
		}
	}

	lat := make([]time.Duration, *n)
	start := time.Now()
	for i := 0; i < *n; i++ {
		qs := time.Now()
		if _, err := conn.ExecuteFetch(*query, 10001, true); err != nil {
			fmt.Fprintf(os.Stderr, "exec: %v\n", err)
			os.Exit(1)
		}
		lat[i] = time.Since(qs)
	}
	total := time.Since(start)

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration { return lat[int(float64(*n)*p)] }
	fmt.Printf("n=%d total=%v mean=%.1fus p50=%.1fus p90=%.1fus p99=%.1fus max=%.1fus\n",
		*n, total,
		float64(total.Microseconds())/float64(*n),
		float64(pct(0.50).Nanoseconds())/1000,
		float64(pct(0.90).Nanoseconds())/1000,
		float64(pct(0.99).Nanoseconds())/1000,
		float64(lat[*n-1].Nanoseconds())/1000)
}
