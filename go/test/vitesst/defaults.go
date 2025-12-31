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

const (
	// DefaultCell is the default cell name.
	DefaultCell = "zone1"

	// DefaultMySQLVersion is the default MySQL version.
	DefaultMySQLVersion = "8.0"

	// DefaultShardCount is the default number of shards (unsharded).
	DefaultShardCount = 1

	// DefaultReplicaCount is the default number of tablets per shard.
	// This count includes the primary: 1 = primary only, 2 = primary + 1 replica, etc.
	DefaultReplicaCount = 1

	// DefaultDurabilityPolicy is the default durability policy.
	DefaultDurabilityPolicy = "none"

	// DefaultVTGateMySQLPort is the default MySQL protocol port for vtgate.
	DefaultVTGateMySQLPort = 15306

	// DefaultTopoImplementation is the default topology server implementation.
	DefaultTopoImplementation = "etcd2"

	// vitesstImage is the prebuilt image of the current source.
	vitesstImage = "vitesst:latest"

	// topoGlobalRoot is the global root path in etcd.
	topoGlobalRoot = "/vitess/global"

	// networkName is the Docker network name prefix.
	networkName = "vitesst-net"

	// imageName is the Docker image name for Vitess components.
	imageName = "vitesst"
)
