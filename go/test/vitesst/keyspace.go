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

// keyspaceBuilder provides a fluent API for configuring a keyspace.
// Create one with WithKeyspace() and chain methods to configure it.
//
// Example:
//
//	WithKeyspace("ks").
//	    WithSchema(`CREATE TABLE t1 (id INT PRIMARY KEY)`).
//	    WithVSchema(`{"sharded": false, "tables": {"t1": {}}}`)
type keyspaceBuilder struct {
	config keyspaceConfig
}

// WithKeyspace creates a new keyspace builder with the given name.
func WithKeyspace(name string) *keyspaceBuilder {
	return &keyspaceBuilder{
		config: keyspaceConfig{
			name:             name,
			shardCount:       defaultShardCount,
			replicaCount:     defaultReplicaCount,
			durabilityPolicy: defaultDurabilityPolicy,
		},
	}
}

// apply implements ClusterOption interface.
func (kb *keyspaceBuilder) apply(opts *clusterOptions) {
	opts.keyspaces = append(opts.keyspaces, kb.config)
}

// WithSchema sets the SQL schema for the keyspace.
func (kb *keyspaceBuilder) WithSchema(sql string) *keyspaceBuilder {
	kb.config.schema = sql
	return kb
}

// WithVSchema sets the VSchema JSON for the keyspace.
func (kb *keyspaceBuilder) WithVSchema(json string) *keyspaceBuilder {
	kb.config.vschema = json
	return kb
}

// WithShardCount sets the number of shards (auto-generates ranges).
func (kb *keyspaceBuilder) WithShardCount(n int) *keyspaceBuilder {
	kb.config.shardCount = n
	return kb
}

// WithReplicaCount sets the number of tablets per shard.
func (kb *keyspaceBuilder) WithReplicaCount(n int) *keyspaceBuilder {
	kb.config.replicaCount = n
	return kb
}

// WithDurabilityPolicy sets the durability policy.
func (kb *keyspaceBuilder) WithDurabilityPolicy(policy string) *keyspaceBuilder {
	kb.config.durabilityPolicy = policy
	return kb
}
