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
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startVTTablet starts a vttablet container with MySQL in the specified cell.
func (c *cluster) startVTTablet(ctx context.Context, keyspace, shard string, uid int, cell string) (testcontainers.Container, error) {
	httpPort := 15100 + uid
	grpcPort := 16100 + uid
	alias := fmt.Sprintf("vttablet-%s-%s-%d", keyspace, shard, uid)

	startupScript := fmt.Sprintf(`#!/bin/bash
set -ex
mysqlctl --tablet-uid %d --mysql-port 3306 init
exec vttablet \
  --topo-implementation %s \
  --topo-global-server-address etcd:2379 \
  --topo-global-root %s \
  --tablet-path %s-%d \
  --init-keyspace %s \
  --init-shard %s \
  --init-tablet-type replica \
  --port %d \
  --grpc-port %d \
  --service-map 'grpc-queryservice,grpc-tabletmanager,grpc-updatestream' \
  --enable-replication-reporter \
  %s
`, uid, defaultTopoImplementation, topoGlobalRoot, cell, uid, keyspace, shard, httpPort, grpcPort, strings.Join(c.opts.vttabletArgs, " "))

	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      c.vitessImage,
			Entrypoint: []string{"bash", "-c", startupScript},
			ExposedPorts: []string{
				fmt.Sprintf("%d/tcp", httpPort),
				fmt.Sprintf("%d/tcp", grpcPort),
				"3306/tcp",
			},
			Networks: []string{c.network.Name},
			NetworkAliases: map[string][]string{
				c.network.Name: {alias},
			},
			WaitingFor: waitForVTTablet(fmt.Sprintf("%d/tcp", httpPort)),
		},
		Started: true,
	})
}

// waitForVTTablet returns a wait strategy for vttablet readiness.
// vttablet is ready when the HTTP server is listening.
// We use /debug/status instead of /debug/health because /debug/health
// requires the tablet to be fully serving (which requires a primary to
// be elected and the keyspace database to exist).
func waitForVTTablet(httpPort string) wait.Strategy {
	return wait.ForHTTP("/debug/status").
		WithPort(nat.Port(httpPort)).
		WithStartupTimeout(defaultStartupTimeout).
		WithPollInterval(defaultPollInterval)
}
