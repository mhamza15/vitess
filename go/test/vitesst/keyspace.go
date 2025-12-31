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

// keyspaceConfig holds keyspace configuration.
type keyspaceConfig struct {
	name             string
	schema           string
	vschema          string
	shardCount       int
	replicaCount     int
	durabilityPolicy string
}

// KeyspaceBuilder provides a fluent API for configuring a keyspace.
// Create one with WithKeyspace() and chain methods to configure it.
//
// Example:
//
//	WithKeyspace("ks").
//	    WithSchema(`CREATE TABLE t1 (id INT PRIMARY KEY)`).
//	    WithVSchema(`{"sharded": false, "tables": {"t1": {}}}`)
type KeyspaceBuilder struct {
	config keyspaceConfig
}

// WithKeyspace creates a new keyspace builder with the given name.
func WithKeyspace(name string) *KeyspaceBuilder {
	return &KeyspaceBuilder{
		config: keyspaceConfig{
			name:             name,
			shardCount:       DefaultShardCount,
			replicaCount:     DefaultReplicaCount,
			durabilityPolicy: DefaultDurabilityPolicy,
		},
	}
}

// apply implements ClusterOption interface.
func (kb *KeyspaceBuilder) apply(opts *clusterOptions) {
	opts.keyspaces = append(opts.keyspaces, kb.config)
}

// WithSchema sets the SQL schema for the keyspace.
func (kb *KeyspaceBuilder) WithSchema(sql string) *KeyspaceBuilder {
	kb.config.schema = sql
	return kb
}

// WithVSchema sets the VSchema JSON for the keyspace.
func (kb *KeyspaceBuilder) WithVSchema(json string) *KeyspaceBuilder {
	kb.config.vschema = json
	return kb
}

// WithShardCount sets the number of shards (auto-generates ranges).
// 1 = unsharded ("-"), 2 = "-80", "80-", 4 = "-40", "40-80", "80-c0", "c0-", etc.
func (kb *KeyspaceBuilder) WithShardCount(n int) *KeyspaceBuilder {
	kb.config.shardCount = n
	return kb
}

// WithReplicaCount sets the number of tablets per shard.
// This count includes the primary: 1 = primary only, 2 = primary + 1 replica, etc.
// Default is 1 (primary only).
func (kb *KeyspaceBuilder) WithReplicaCount(n int) *KeyspaceBuilder {
	kb.config.replicaCount = n
	return kb
}

// WithDurabilityPolicy sets the durability policy.
func (kb *KeyspaceBuilder) WithDurabilityPolicy(policy string) *KeyspaceBuilder {
	kb.config.durabilityPolicy = policy
	return kb
}
