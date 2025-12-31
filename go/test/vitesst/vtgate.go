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

// VTGate port constants.
const (
	vtgateHTTPPort = 15001
	vtgateGRPCPort = 15999
)

// startVTGate starts the vtgate container.
func (c *Cluster) startVTGate(ctx context.Context) (testcontainers.Container, error) {
	args := []string{
		"vtgate",
		"--topo-implementation", DefaultTopoImplementation,
		"--topo-global-server-address", "etcd:2379",
		"--topo-global-root", topoGlobalRoot,
		"--cell", c.cell,
		"--cells-to-watch", c.cell,
		"--port", strconv.Itoa(vtgateHTTPPort),
		"--grpc-port", strconv.Itoa(vtgateGRPCPort),
		"--mysql-server-port", strconv.Itoa(DefaultVTGateMySQLPort),
		"--mysql-auth-server-impl", "none",
		"--tablet-types-to-wait", "PRIMARY,REPLICA",
	}
	args = append(args, c.opts.vtgateArgs...)

	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: c.vitessImage,
			Cmd:   args,
			ExposedPorts: []string{
				fmt.Sprintf("%d/tcp", vtgateHTTPPort),
				fmt.Sprintf("%d/tcp", vtgateGRPCPort),
				fmt.Sprintf("%d/tcp", DefaultVTGateMySQLPort),
			},
			Networks: []string{c.network.Name},
			NetworkAliases: map[string][]string{
				c.network.Name: {"vtgate"},
			},
			WaitingFor: waitForVTGate(),
		},
		Started: true,
	})
}

// waitForVTGate returns a wait strategy for vtgate readiness.
// vtgate is ready when the MySQL protocol port is listening.
func waitForVTGate() wait.Strategy {
	return wait.ForListeningPort("15306/tcp").
		WithStartupTimeout(defaultStartupTimeout).
		WithPollInterval(defaultPollInterval)
}
