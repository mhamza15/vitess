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
	// defaultCell is the default cell name.
	defaultCell = "zone1"

	// defaultMySQLVersion is the default MySQL version.
	defaultMySQLVersion = "8.0"

	// defaultShardCount is the default number of shards (unsharded).
	defaultShardCount = 1

	// defaultReplicaCount is the default number of tablets per shard.
	// This count includes the primary: 1 = primary only, 2 = primary + 1 replica, etc.
	defaultReplicaCount = 1

	// defaultDurabilityPolicy is the default durability policy.
	defaultDurabilityPolicy = "none"

	// defaultVTGateMySQLPort is the default MySQL protocol port for vtgate.
	defaultVTGateMySQLPort = 15306

	// defaultTopoImplementation is the default topology server implementation.
	defaultTopoImplementation = "etcd2"

	// vitesstImage is the prebuilt image of the current source.
	vitesstImage = "vitesst:latest"

	// topoGlobalRoot is the global root path in etcd.
	topoGlobalRoot = "/vitess/global"

	// networkName is the Docker network name prefix.
	networkName = "vitesst-net"
)
