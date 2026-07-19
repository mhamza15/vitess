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

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPercentile(t *testing.T) {
	assert.Equal(t, time.Duration(0), percentile(nil))
	assert.Equal(t, 5*time.Second, percentile([]float64{5}))
	// 85th percentile of ten observations is the ninth smallest.
	obs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}
	assert.Equal(t, 9*time.Second, percentile(obs))
}

func TestTestFilter(t *testing.T) {
	assert.Equal(t, "^(TestA|TestB)$", testFilter([]string{"TestA", "TestB"}))
}

func TestSplitPackageCoversEveryTest(t *testing.T) {
	tests := map[string]time.Duration{
		"TestA": 10 * time.Minute,
		"TestB": 6 * time.Minute,
		"TestC": 5 * time.Minute,
		"TestD": 3 * time.Minute,
	}
	units := splitPackage("pkg", 24*time.Minute, tests, 8*time.Minute)
	require.Len(t, units, 3)

	// Exactly one slice carries the skip complement, and the run and skip
	// filters mention each test exactly once between them.
	var runFlags, skipFlags []string
	for _, u := range units {
		assert.Equal(t, "pkg", u.pkg)
		switch {
		case strings.HasPrefix(u.runFlags, "-run "):
			runFlags = append(runFlags, u.runFlags)
		case strings.HasPrefix(u.runFlags, "-skip "):
			skipFlags = append(skipFlags, u.runFlags)
		}
	}
	require.Len(t, skipFlags, 1)
	require.Len(t, runFlags, 2)
	// Each test appears in at most one run filter, and the skip filter is
	// exactly the union of the run filters, so a test either runs in its one
	// run slice or falls through every filter into the skip slice.
	for name := range tests {
		runCount := 0
		for _, f := range runFlags {
			runCount += strings.Count(f, name)
		}
		assert.LessOrEqual(t, runCount, 1, "test %s in more than one run filter", name)
		skipCount := strings.Count(skipFlags[0], name)
		assert.Equal(t, runCount, skipCount, "test %s must be skipped iff it has a run slice", name)
	}
}

func TestPackKeepsSlicesAlone(t *testing.T) {
	units := []unit{
		{pkg: "big", cost: 10 * time.Minute, runFlags: "-run '^(TestA)$'", slice: "1/2"},
		{pkg: "big", cost: 9 * time.Minute, runFlags: "-skip '^(TestA)$'", slice: "2/2"},
		{pkg: "small1", cost: 3 * time.Minute},
		{pkg: "small2", cost: 2 * time.Minute},
		{pkg: "small3", cost: 1 * time.Minute},
	}
	buckets := pack(units, 4)
	require.Len(t, buckets, 4)

	for _, b := range buckets {
		if len(b.units) > 0 && b.units[0].runFlags != "" {
			assert.Len(t, b.units, 1, "sliced units must not share a partition")
		}
	}
}

func TestBuildUnitsNominalForUnknown(t *testing.T) {
	units := buildUnits([]string{"known", "unknown"},
		map[string]map[string][]float64{"known": {"TestA": {60}}},
		map[string][]float64{"known": {90}},
		5*time.Minute, 4)
	require.Len(t, units, 2)
	byPkg := map[string]unit{}
	for _, u := range units {
		byPkg[u.pkg] = u
	}
	assert.Equal(t, 90*time.Second, byPkg["known"].cost)
	assert.Equal(t, 5*time.Minute, byPkg["unknown"].cost)
}
