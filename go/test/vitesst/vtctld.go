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
	"context"
	"fmt"
	"strconv"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// VTCtld port constants.
const (
	vtctldHTTPPort = 15000
	vtctldGRPCPort = 15999
)

// startVTCtld starts the vtctld container.
func (c *Cluster) startVTCtld(ctx context.Context) (testcontainers.Container, error) {
	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: c.vitessImage,
			Cmd: []string{
				"vtctld",
				"--topo-implementation", DefaultTopoImplementation,
				"--topo-global-server-address", "etcd:2379",
				"--topo-global-root", topoGlobalRoot,
				"--cell", c.cell,
				"--service-map", "grpc-vtctl,grpc-vtctld",
				"--port", strconv.Itoa(vtctldHTTPPort),
				"--grpc-port", strconv.Itoa(vtctldGRPCPort),
			},
			ExposedPorts: []string{
				fmt.Sprintf("%d/tcp", vtctldHTTPPort),
				fmt.Sprintf("%d/tcp", vtctldGRPCPort),
			},
			Networks: []string{c.network.Name},
			NetworkAliases: map[string][]string{
				c.network.Name: {"vtctld"},
			},
			WaitingFor: waitForVTCtld(),
		},
		Started: true,
	})
}

// vtctldExec runs a vtctldclient command.
func (c *Cluster) vtctldExec(ctx context.Context, args ...string) error {
	cmd := append([]string{"vtctldclient", "--server", fmt.Sprintf("vtctld:%d", vtctldGRPCPort)}, args...)

	exitCode, output, err := c.vtctld.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("exec failed: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("command failed with exit code %d: %s", exitCode, output)
	}

	return nil
}

// initCell initializes the cell in the topology server.
func (c *Cluster) initCell(ctx context.Context) error {
	return c.vtctldExec(ctx, "AddCellInfo", "--root", "/vitess/"+c.cell, "--server-address", "etcd:2379", c.cell)
}

// waitForVTCtld returns a wait strategy for vtctld readiness.
// vtctld is ready when the HTTP health endpoint returns successfully.
func waitForVTCtld() wait.Strategy {
	return wait.ForHTTP("/debug/vars").
		WithPort("15000/tcp").
		WithStartupTimeout(defaultStartupTimeout).
		WithPollInterval(defaultPollInterval)
}
