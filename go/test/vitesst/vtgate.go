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
	"strconv"
	"strings"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// VTGate port constants.
const (
	vtgateHTTPPort = 15001
	vtgateGRPCPort = 15999
)

// startVTGate starts the vtgate container.
func (c *cluster) startVTGate(t *testing.T) (testcontainers.Container, error) {
	args := []string{
		"vtgate",
		"--topo-implementation", defaultTopoImplementation,
		"--topo-global-server-address", "etcd:2379",
		"--topo-global-root", topoGlobalRoot,
		"--cell", c.cells[0],
		"--cells-to-watch", strings.Join(c.cells, ","),
		"--port", strconv.Itoa(vtgateHTTPPort),
		"--grpc-port", strconv.Itoa(vtgateGRPCPort),
		"--mysql-server-port", strconv.Itoa(defaultVTGateMySQLPort),
		"--mysql-auth-server-impl", "none",
		"--tablet-types-to-wait", "PRIMARY",
		"--logtostderr",
	}
	args = append(args, c.opts.vtgateArgs...)

	return testcontainers.Run(t.Context(), c.vitessImage,
		testcontainers.WithCmd(args...),
		testcontainers.WithExposedPorts(
			fmt.Sprintf("%d/tcp", vtgateHTTPPort),
			fmt.Sprintf("%d/tcp", vtgateGRPCPort),
			fmt.Sprintf("%d/tcp", defaultVTGateMySQLPort),
		),
		network.WithNetwork([]string{"vtgate"}, c.network),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/debug/status").
				WithPort(nat.Port(fmt.Sprintf("%d/tcp", vtgateHTTPPort))).
				WithStartupTimeout(defaultStartupTimeout).
				WithPollInterval(defaultPollInterval),
		),
		testcontainers.WithLogger(tclog.TestLogger(t)),
	)
}
