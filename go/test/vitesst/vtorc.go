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
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

type vtorcLogFollowingOption struct{}

func (vtorcLogFollowingOption) apply(opts *clusterOptions) {
	opts.vtorcLogFollowing = true
}

// WithVTOrcLogger enables following vtorc container logs to test output.
func WithVTOrcLogger() ClusterOption {
	return vtorcLogFollowingOption{}
}

// VTOrc port constants.
const (
	vtorcHTTPPort = 16000
)

// startVTOrc starts the VTOrc container for automated failover management.
func (c *cluster) startVTOrc(t *testing.T) (testcontainers.Container, error) {
	// VTOrc command with minimal configuration for test environments.
	// VTOrc monitors tablets and handles automated failover.
	args := []string{
		"vtorc",
		"--topo-implementation", defaultTopoImplementation,
		"--topo-global-server-address", "etcd:2379",
		"--topo-global-root", topoGlobalRoot,
		"--cell", c.cells[0],
		"--port", strconv.Itoa(vtorcHTTPPort),
		"--instance-poll-time", "1s",
		"--topo-information-refresh-duration", "3s",
		"--alsologtostderr",
	}

	// Append user-provided VTOrc args
	args = append(args, c.opts.vtorcArgs...)

	containerOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithCmd(args...),
		testcontainers.WithExposedPorts(fmt.Sprintf("%d/tcp", vtorcHTTPPort)),
		network.WithNetwork([]string{"vtorc"}, c.network),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/debug/health").
				WithPort(nat.Port(fmt.Sprintf("%d/tcp", vtorcHTTPPort))).
				WithStartupTimeout(defaultStartupTimeout).
				WithPollInterval(defaultPollInterval).
				WithStatusCodeMatcher(func(status int) bool {
					return status == 200
				}),
		),
	}

	if c.opts.vtorcLogFollowing {
		containerOpts = append(containerOpts, testcontainers.WithLogConsumers(&testLogConsumer{prefix: "vtorc"}))
	}

	return testcontainers.Run(t.Context(), c.vitesstImage, containerOpts...)
}
