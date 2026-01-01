/*
Copyright 2025 The Vitess Authors.

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

package vitesst

import (
	"fmt"
	"strings"

	"github.com/testcontainers/testcontainers-go"
)

// testLogConsumer implements testcontainers.LogConsumer and writes container
// logs to test output with a prefix identifying the component.
type testLogConsumer struct {
	prefix string
}

// Accept implements testcontainers.LogConsumer.
func (c *testLogConsumer) Accept(l testcontainers.Log) {
	// Strip trailing newlines to avoid double-spacing in test output
	content := strings.TrimRight(string(l.Content), "\n")
	fmt.Printf("[%s] %s\n", c.prefix, content)
}
