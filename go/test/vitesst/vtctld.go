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
	"io"
	"strconv"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// VTCtld port constants.
const (
	vtctldHTTPPort = 15000
	vtctldGRPCPort = 15999
)

// startVTCtld starts the vtctld container.
func (c *cluster) startVTCtld(t *testing.T) (testcontainers.Container, error) {
	return testcontainers.Run(t.Context(), c.vitesstImage,
		testcontainers.WithCmd(
			"vtctld",
			"--topo-implementation", defaultTopoImplementation,
			"--topo-global-server-address", "etcd:2379",
			"--topo-global-root", topoGlobalRoot,
			"--cell", c.cells[0],
			"--service-map", "grpc-vtctl,grpc-vtctld",
			"--port", strconv.Itoa(vtctldHTTPPort),
			"--grpc-port", strconv.Itoa(vtctldGRPCPort),
		),
		testcontainers.WithExposedPorts(
			fmt.Sprintf("%d/tcp", vtctldHTTPPort),
			fmt.Sprintf("%d/tcp", vtctldGRPCPort),
		),
		network.WithNetwork([]string{"vtctld"}, c.network),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/debug/vars").
				WithPort("15000/tcp").
				WithStartupTimeout(defaultStartupTimeout).
				WithPollInterval(defaultPollInterval),
		),
		testcontainers.WithLogger(log.TestLogger(t)),
	)
}

// vtctldExec runs a vtctldclient command.
func (c *cluster) vtctldExec(t *testing.T, args ...string) error {
	cmd := append([]string{"vtctldclient", "--server", fmt.Sprintf("vtctld:%d", vtctldGRPCPort)}, args...)

	exitCode, outputReader, err := c.vtctld.Exec(t.Context(), cmd)
	if err != nil {
		return fmt.Errorf("exec failed: %w", err)
	}

	if exitCode != 0 {
		output, _ := io.ReadAll(outputReader)
		return fmt.Errorf("command failed with exit code %d: %s", exitCode, string(output))
	}

	return nil
}

// initCell initializes a cell in the topology server.
func (c *cluster) initCell(t *testing.T, cell string) error {
	return c.vtctldExec(t, "AddCellInfo", "--root", "/vitess/"+cell, "--server-address", "etcd:2379", cell)
}
