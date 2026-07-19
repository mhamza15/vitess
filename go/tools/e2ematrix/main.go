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

// e2ematrix reads a list of Go packages from stdin and emits a GitHub Actions
// matrix that balances them across partitions by measured test durations.
//
// Unlike a per-package partitioner, packages too large to fit a balanced
// partition are split into per-test slices, each carrying a -run filter. One
// slice per split package instead carries a -skip filter for the complement,
// so tests unknown to the timing data still run somewhere. Packages with no
// timing data count at a nominal cost until real timings exist.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type (
	// event is the subset of a test2json record the cost model reads.
	event struct {
		Action  string  `json:"Action"`
		Package string  `json:"Package"`
		Test    string  `json:"Test"`
		Elapsed float64 `json:"Elapsed"`
	}

	// unit is one schedulable piece of work: a whole package, or a slice of
	// a split package with a run or skip filter.
	unit struct {
		pkg      string
		cost     time.Duration
		runFlags string
		slice    string
	}

	// partition is one matrix entry in the emitted JSON.
	partition struct {
		ID               string `json:"id"`
		EstimatedRuntime string `json:"estimatedRuntime"`
		Packages         string `json:"packages"`
		RunFlags         string `json:"runFlags"`
		Slice            string `json:"slice,omitempty"`
		Description      string `json:"description"`
	}

	bucket struct {
		total time.Duration
		units []unit
	}
)

func main() {
	partitions := flag.Int("partitions", 0, "number of partitions to create")
	timingDir := flag.String("timing-dir", "", "directory tree holding test2json timing logs")
	nominal := flag.Duration("nominal", 5*time.Minute, "cost assumed for packages with no timing data")
	flag.Parse()

	if *partitions < 2 {
		log.Fatal("-partitions must be at least 2")
	}

	pkgs, err := readPackages(os.Stdin)
	if err != nil {
		log.Fatalf("reading packages: %v", err)
	}
	if len(pkgs) == 0 {
		log.Fatal("no packages on stdin")
	}

	testCosts, pkgCosts, err := readTimings(*timingDir)
	if err != nil {
		log.Fatalf("reading timings: %v", err)
	}

	units := buildUnits(pkgs, testCosts, pkgCosts, *nominal, *partitions)
	buckets := pack(units, *partitions)
	if err := json.NewEncoder(os.Stdout).Encode(render(buckets)); err != nil {
		log.Fatalf("encoding matrix: %v", err)
	}
}

func readPackages(r *os.File) ([]string, error) {
	var pkgs []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			pkgs = append(pkgs, line)
		}
	}
	return pkgs, scanner.Err()
}

// readTimings collects per-test and per-package durations from every .log
// file under dir. Multiple observations of the same test reduce to the 85th
// percentile, so one anomalous run does not dominate.
func readTimings(dir string) (map[string]map[string][]float64, map[string][]float64, error) {
	testObs := map[string]map[string][]float64{}
	pkgObs := map[string][]float64{}
	if dir == "" {
		return testObs, pkgObs, nil
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".log") {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var ev event
			if json.Unmarshal(scanner.Bytes(), &ev) != nil {
				continue
			}
			if ev.Action != "pass" && ev.Action != "fail" {
				continue
			}
			switch {
			case ev.Test == "":
				pkgObs[ev.Package] = append(pkgObs[ev.Package], ev.Elapsed)
			case !strings.Contains(ev.Test, "/"):
				if testObs[ev.Package] == nil {
					testObs[ev.Package] = map[string][]float64{}
				}
				testObs[ev.Package][ev.Test] = append(testObs[ev.Package][ev.Test], ev.Elapsed)
			}
		}
		return scanner.Err()
	})
	return testObs, pkgObs, err
}

// percentile returns the 85th percentile of the observations.
func percentile(times []float64) time.Duration {
	if len(times) == 0 {
		return 0
	}
	sort.Float64s(times)
	r := int(math.Ceil(0.85 * float64(len(times))))
	if r == 0 {
		r = 1
	}
	return time.Duration(times[r-1] * float64(time.Second))
}

// buildUnits turns each package into one unit, splitting packages whose cost
// exceeds the balanced-partition target into per-test slices.
func buildUnits(pkgs []string, testObs map[string]map[string][]float64, pkgObs map[string][]float64, nominal time.Duration, partitions int) []unit {
	type pkgCost struct {
		total time.Duration
		tests map[string]time.Duration
	}

	costs := map[string]pkgCost{}
	var total time.Duration
	for _, pkg := range pkgs {
		tests := map[string]time.Duration{}
		var testSum time.Duration
		for name, times := range testObs[pkg] {
			tests[name] = percentile(append([]float64(nil), times...))
			testSum += tests[name]
		}
		cost := max(percentile(append([]float64(nil), pkgObs[pkg]...)), testSum)
		if cost == 0 {
			cost = nominal
			tests = nil
		}
		costs[pkg] = pkgCost{total: cost, tests: tests}
		total += cost
	}

	target := total / time.Duration(partitions)
	var units []unit
	for _, pkg := range pkgs {
		pc := costs[pkg]
		// Split only when the package provably cannot fit a balanced
		// partition and per-test costs are known.
		if pc.total <= target*6/5 || len(pc.tests) < 2 {
			units = append(units, unit{pkg: pkg, cost: pc.total})
			continue
		}
		units = append(units, splitPackage(pkg, pc.total, pc.tests, target)...)
	}
	return units
}

// splitPackage packs a package's tests into ceil(cost/target) slices. Every
// slice except the last carries an exact -run filter; the last carries the
// complement as a -skip filter, so new tests missing from the timing data
// still run.
func splitPackage(pkg string, total time.Duration, tests map[string]time.Duration, target time.Duration) []unit {
	k := max(int(math.Ceil(float64(total)/float64(target))), 2)
	k = min(k, len(tests))

	names := make([]string, 0, len(tests))
	for name := range tests {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if tests[names[i]] != tests[names[j]] {
			return tests[names[i]] > tests[names[j]]
		}
		return names[i] < names[j]
	})

	slices := make([]bucket, k)
	for _, name := range names {
		minIdx := 0
		for i := range slices {
			if slices[i].total < slices[minIdx].total {
				minIdx = i
			}
		}
		slices[minIdx].total += tests[name]
		slices[minIdx].units = append(slices[minIdx].units, unit{pkg: name, cost: tests[name]})
	}

	units := make([]unit, 0, k)
	var explicit []string
	for i, s := range slices[:k-1] {
		var sliceNames []string
		for _, u := range s.units {
			sliceNames = append(sliceNames, u.pkg)
		}
		sort.Strings(sliceNames)
		explicit = append(explicit, sliceNames...)
		units = append(units, unit{
			pkg:      pkg,
			cost:     s.total,
			runFlags: fmt.Sprintf("-run '%s'", testFilter(sliceNames)),
			slice:    fmt.Sprintf("%d/%d", i+1, k),
		})
	}
	sort.Strings(explicit)
	units = append(units, unit{
		pkg:      pkg,
		cost:     slices[k-1].total,
		runFlags: fmt.Sprintf("-skip '%s'", testFilter(explicit)),
		slice:    fmt.Sprintf("%d/%d", k, k),
	})
	return units
}

// testFilter builds an anchored regexp matching exactly the given top-level
// test names.
func testFilter(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return fmt.Sprintf("^(%s)$", strings.Join(quoted, "|"))
}

// pack places units into partitions, largest first, each into the currently
// least-loaded partition. Sliced units never share a partition with other
// packages: their -run filter would apply to every package in the shared go
// test invocation.
func pack(units []unit, partitions int) []bucket {
	var sliced, whole []unit
	for _, u := range units {
		if u.runFlags != "" {
			sliced = append(sliced, u)
		} else {
			whole = append(whole, u)
		}
	}
	if len(sliced) >= partitions {
		log.Fatalf("%d sliced units for %d partitions; raise -partitions", len(sliced), partitions)
	}

	buckets := make([]bucket, 0, partitions)
	for _, u := range sliced {
		buckets = append(buckets, bucket{total: u.cost, units: []unit{u}})
	}

	rest := make([]bucket, partitions-len(sliced))
	sort.Slice(whole, func(i, j int) bool {
		if whole[i].cost != whole[j].cost {
			return whole[i].cost > whole[j].cost
		}
		return whole[i].pkg < whole[j].pkg
	})
	for _, u := range whole {
		minIdx := 0
		for i := range rest {
			if rest[i].total < rest[minIdx].total {
				minIdx = i
			}
		}
		rest[minIdx].total += u.cost
		rest[minIdx].units = append(rest[minIdx].units, u)
	}
	return append(buckets, rest...)
}

func render(buckets []bucket) map[string][]partition {
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].total > buckets[j].total })

	out := make([]partition, 0, len(buckets))
	for _, b := range buckets {
		if len(b.units) == 0 {
			continue
		}
		pkgs := make([]string, len(b.units))
		for i, u := range b.units {
			pkgs[i] = u.pkg
		}
		p := partition{
			ID:               strconv.Itoa(len(out)),
			EstimatedRuntime: b.total.Round(time.Second).String(),
			Packages:         strings.Join(pkgs, " "),
			RunFlags:         b.units[0].runFlags,
			Slice:            b.units[0].slice,
			Description:      fmt.Sprintf("%d - %s", len(out), pkgs[0]),
		}
		out = append(out, p)
	}
	return map[string][]partition{"include": out}
}
