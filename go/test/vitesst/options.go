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

// clusterOptions holds all cluster configuration.
type clusterOptions struct {
	keyspaces    []keyspaceConfig
	cells        []string
	vtgateArgs   []string
	vttabletArgs []string
	vtctldArgs   []string
	vtorcEnabled bool
	vtorcArgs    []string
	mysqlVersion string

	// Log following options - when enabled, container logs are piped to test output.
	vtgateLogFollowing   bool
	vttabletLogFollowing bool
	vtctldLogFollowing   bool
	vtorcLogFollowing    bool
	etcdLogFollowing     bool
}

// ClusterOption configures the cluster.
type ClusterOption interface {
	apply(*clusterOptions)
}

type cellsOption []string

func (c cellsOption) apply(opts *clusterOptions) {
	opts.cells = c
}

// WithCells sets the cell names for the cluster.
// If not provided, defaults to a single cell named "zone1".
// Tablets are distributed across cells in a round-robin fashion.
func WithCells(cells ...string) ClusterOption {
	return cellsOption(cells)
}

type vtgateArgsOption []string

func (a vtgateArgsOption) apply(opts *clusterOptions) {
	opts.vtgateArgs = append(opts.vtgateArgs, a...)
}

// WithVTGateArgs adds extra arguments to vtgate.
func WithVTGateArgs(args ...string) ClusterOption {
	return vtgateArgsOption(args)
}

type vttabletArgsOption []string

func (a vttabletArgsOption) apply(opts *clusterOptions) {
	opts.vttabletArgs = append(opts.vttabletArgs, a...)
}

// WithVTTabletArgs adds extra arguments to all vttablets.
func WithVTTabletArgs(args ...string) ClusterOption {
	return vttabletArgsOption(args)
}

type vtctldArgsOption []string

func (a vtctldArgsOption) apply(opts *clusterOptions) {
	opts.vtctldArgs = append(opts.vtctldArgs, a...)
}

// WithVTCtldArgs adds extra arguments to vtctld.
func WithVTCtldArgs(args ...string) ClusterOption {
	return vtctldArgsOption(args)
}

type vtorcOption struct {
	args []string
}

func (v vtorcOption) apply(opts *clusterOptions) {
	opts.vtorcEnabled = true
	opts.vtorcArgs = append(opts.vtorcArgs, v.args...)
}

// WithVTOrc enables VTOrc for the cluster.
func WithVTOrc(args ...string) ClusterOption {
	return vtorcOption{args: args}
}

type mysqlVersionOption string

func (m mysqlVersionOption) apply(opts *clusterOptions) {
	opts.mysqlVersion = string(m)
}

// WithMySQLVersion sets the MySQL version for the cluster.
// This affects which Docker image tag is used (e.g., "vitesst:mysql84").
// Valid values are "8.0" and "8.4". Defaults to "8.4".
func WithMySQLVersion(version string) ClusterOption {
	return mysqlVersionOption(version)
}

// defaultClusterOptions returns a clusterOptions with all defaults applied.
func defaultClusterOptions() *clusterOptions {
	return &clusterOptions{
		cells:        []string{defaultCell},
		mysqlVersion: defaultMySQLVersion,
	}
}
