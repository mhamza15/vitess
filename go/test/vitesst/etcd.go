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

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// etcd constants.
const (
	// etcdImage is the official etcd Docker image.
	etcdImage = "quay.io/coreos/etcd:v3.5.21"

	// etcdClientPort is the etcd client port.
	etcdClientPort = 2379
)

// startEtcd starts the etcd container using the official etcd image.
func (c *Cluster) startEtcd(ctx context.Context) (testcontainers.Container, error) {
	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: etcdImage,
			Cmd: []string{
				"etcd",
				"--listen-client-urls", "http://0.0.0.0:2379",
				"--advertise-client-urls", "http://etcd:2379",
			},
			ExposedPorts: []string{"2379/tcp"},
			Networks:     []string{c.network.Name},
			NetworkAliases: map[string][]string{
				c.network.Name: {"etcd"},
			},
			WaitingFor: waitForEtcd(),
		},
		Started: true,
	})
}

// waitForEtcd returns a wait strategy for etcd readiness.
// etcd is ready when the client port is listening.
func waitForEtcd() wait.Strategy {
	return wait.ForListeningPort("2379/tcp").
		WithStartupTimeout(defaultStartupTimeout).
		WithPollInterval(defaultPollInterval)
}
