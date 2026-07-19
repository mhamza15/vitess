/*
Copyright 2026 The Vitess Authors.

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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"

	"vitess.io/vitess/go/mysql"
	"vitess.io/vitess/go/vt/grpcclient"
	"vitess.io/vitess/go/vt/vtgate/grpcvtgateconn"
	"vitess.io/vitess/go/vt/vtgate/vtgateconn"
)

type (
	// VTGate is the runtime handle for one vtgate container. Restart swaps
	// the container behind the handle; address accessors re-resolve mapped
	// ports on every call.
	VTGate struct {
		component

		specMu sync.Mutex
		spec   VTGateSpec
	}

	// VTGateSpec describes a vtgate to start. An empty Cell places it in the
	// cluster's first cell, and an empty CellsToWatch makes it watch every
	// cell of the cluster. CellsToWatch also accepts a cell alias.
	VTGateSpec struct {
		Cell         string
		CellsToWatch []string
		ExtraArgs    []string
	}
)

// MySQLAddr returns the host-reachable "host:port" of the vtgate MySQL
// listener.
func (vtg *VTGate) MySQLAddr(ctx context.Context) (string, error) {
	return vtg.hostAddr(ctx, fmt.Sprintf("%d/tcp", vtgateMySQLPort))
}

// Connect returns a new MySQL connection to this vtgate with no default
// database selected.
func (vtg *VTGate) Connect(ctx context.Context) (*mysql.Conn, error) {
	addr, err := vtg.MySQLAddr(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving %s mysql address: %w", vtg.name, err)
	}

	host, portStr, _ := strings.Cut(addr, ":")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("parsing %s mysql port from %q: %w", vtg.name, addr, err)
	}

	params := mysql.ConnParams{Host: host, Port: port}
	conn, err := mysql.Connect(ctx, &params)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", vtg.name, err)
	}
	return conn, nil
}

// GRPCAddr returns the host-reachable "host:port" of the vtgate gRPC port.
func (vtg *VTGate) GRPCAddr(ctx context.Context) (string, error) {
	return vtg.hostAddr(ctx, fmt.Sprintf("%d/tcp", vtgateGRPCPort))
}

// DialVTGate returns a vtgateconn connected to this vtgate over gRPC.
func (vtg *VTGate) DialVTGate(ctx context.Context) (*vtgateconn.VTGateConn, error) {
	addr, err := vtg.GRPCAddr(ctx)
	if err != nil {
		return nil, err
	}
	return vtgateconn.DialProtocol(ctx, "grpc", addr)
}

// dialerSeq hands out a unique protocol name for each credentialed dialer, so
// concurrent DialVTGateAs calls with different credentials never share a
// registry entry.
var dialerSeq atomic.Uint64

// DialVTGateAs returns a vtgateconn connected to this vtgate over gRPC,
// authenticating as the given static-auth user. An empty username and password
// dials without credentials, so the vtgate rejects the unauthenticated client.
func (vtg *VTGate) DialVTGateAs(ctx context.Context, username, password string) (*vtgateconn.VTGateConn, error) {
	addr, err := vtg.GRPCAddr(ctx)
	if err != nil {
		return nil, err
	}

	creds := grpc.WithPerRPCCredentials(&grpcclient.StaticAuthClientCreds{Username: username, Password: password})
	protocol := fmt.Sprintf("grpc-auth-%d", dialerSeq.Add(1))
	vtgateconn.RegisterDialer(protocol, grpcvtgateconn.Dial(creds))
	return vtgateconn.DialProtocol(ctx, protocol, addr)
}

// ReadVSchema fetches and decodes the vtgate's /debug/vschema.
func (vtg *VTGate) ReadVSchema(ctx context.Context) (*any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	status, body, err := vtg.MakeAPICall(ctx, "/debug/vschema")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%s /debug/vschema returned status %d", vtg.name, status)
	}

	var results any
	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, fmt.Errorf("decoding %s /debug/vschema: %w", vtg.name, err)
	}
	return &results, nil
}

// WriteConfig replaces the vtgate's watched config file, so the running
// vtgate hot-reloads the new values. Poll /debug/config through MakeAPICall
// to observe the reload.
func (vtg *VTGate) WriteConfig(ctx context.Context, content string) error {
	ctr := vtg.container()
	if ctr == nil {
		return fmt.Errorf("%s has no container", vtg.name)
	}
	return writeContainerFile(ctx, ctr, vtgateConfigPath, content)
}

// QueryLog returns the vtgate's query log content so far.
func (vtg *VTGate) QueryLog(ctx context.Context) (string, error) {
	ctr := vtg.container()
	if ctr == nil {
		return "", fmt.Errorf("%s has no container", vtg.name)
	}
	_, output, err := containerExec(ctx, ctr, []string{"cat", vtgateQueryLogPath})
	if err != nil {
		return "", fmt.Errorf("reading %s query log: %w", vtg.name, err)
	}
	return output, nil
}

// Restart recreates the vtgate container behind this handle with the same
// network alias. When extraArgs are given they replace the vtgate's previous
// extra args, so tests can restart vtgate with new flags. Mapped host ports
// change across a restart; use the address accessors to re-resolve them.
func (vtg *VTGate) Restart(t testing.TB, ctx context.Context, extraArgs ...string) error {
	vtg.specMu.Lock()
	if len(extraArgs) > 0 {
		vtg.spec.ExtraArgs = extraArgs
	}
	spec := vtg.spec
	vtg.specMu.Unlock()

	old := vtg.setContainer(nil)
	if old != nil {
		if err := testcontainers.TerminateContainer(old, testcontainers.StopContext(ctx), testcontainers.StopTimeout(0)); err != nil {
			return fmt.Errorf("terminating %s for restart: %w", vtg.name, err)
		}
	}

	ctr, err := vtg.cluster.runVTGateContainer(t, ctx, vtg.name, spec)
	if err != nil {
		return fmt.Errorf("restarting %s: %w", vtg.name, err)
	}
	vtg.setContainer(ctr)
	return nil
}

// VTGate returns the cluster's first vtgate.
func (c *Cluster) VTGate() *VTGate {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.vtgates) == 0 {
		return nil
	}
	return c.vtgates[0]
}

// AddVTGate starts an additional vtgate with its own network alias for
// multi-vtgate tests, in the cluster's first cell and watching every cell. The
// given extraArgs apply to it in place of the cluster-wide vtgate args.
func (c *Cluster) AddVTGate(t testing.TB, ctx context.Context, extraArgs ...string) (*VTGate, error) {
	return c.AddVTGateSpec(t, ctx, VTGateSpec{ExtraArgs: extraArgs})
}

// AddVTGateSpec starts an additional vtgate with its own network alias, placed
// in the spec's cell and watching the spec's cells.
func (c *Cluster) AddVTGateSpec(t testing.TB, ctx context.Context, spec VTGateSpec) (*VTGate, error) {
	if spec.Cell == "" {
		spec.Cell = c.firstCell()
	}
	if len(spec.CellsToWatch) == 0 {
		spec.CellsToWatch = c.cellNames()
	}
	if len(spec.ExtraArgs) == 0 {
		spec.ExtraArgs = c.opts.vtgateArgs
	}

	c.mu.Lock()
	index := c.vtgateSeq
	c.vtgateSeq++
	c.mu.Unlock()

	name := c.name("vtgate")
	if index > 0 {
		name = c.name(fmt.Sprintf("vtgate-%d", index+1))
	}

	g := &VTGate{
		component: component{
			name:     name,
			httpPort: fmt.Sprintf("%d/tcp", vtgateHTTPPort),
			cluster:  c,
		},
		spec: spec,
	}

	ctr, err := c.runVTGateContainer(t, ctx, name, spec)
	if err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	g.setContainer(ctr)

	c.mu.Lock()
	c.vtgates = append(c.vtgates, g)
	c.mu.Unlock()
	return g, nil
}

// runVTGateContainer starts one vtgate container with the given network alias,
// from a spec whose cell and watched cells are already resolved.
func (c *Cluster) runVTGateContainer(t testing.TB, ctx context.Context, name string, spec VTGateSpec) (testcontainers.Container, error) {
	args := []string{"vtgate"}
	args = append(args, c.TopoFlags()...)
	args = append(
		args,
		"--config-file", vtgateConfigPath,
		"--log-queries-to-file", vtgateQueryLogPath,
		"--cell", spec.Cell,
		"--cells-to-watch", strings.Join(spec.CellsToWatch, ","),
		"--port", strconv.Itoa(vtgateHTTPPort),
		"--grpc-port", strconv.Itoa(vtgateGRPCPort),
		"--mysql-server-port", strconv.Itoa(vtgateMySQLPort),
		"--mysql-auth-server-impl", "none",
		"--tablet-types-to-wait", "PRIMARY",
		"--service-map", "grpc-tabletmanager,grpc-throttler,grpc-queryservice,grpc-updatestream,grpc-vtctl,grpc-vtgateservice",
		"--log-format", "text",
		"--alsologtostderr",
	)
	if c.mysqlVersion != "" && !argsContain(spec.ExtraArgs, "mysql-server-version") {
		args = append(args, "--mysql-server-version", c.mysqlVersion+"-vitess")
	}
	args = append(args, spec.ExtraArgs...)

	// The config file is staged world-writable: files are copied in as root,
	// and WriteConfig overwrites this path by exec as the vitess user.
	files := append([]ContainerFile{{Content: []byte("{}\n"), ContainerPath: vtgateConfigPath, Mode: 0o666}}, c.opts.vtgateFiles...)
	filesOpt, err := withContainerFiles(files)
	if err != nil {
		return nil, fmt.Errorf("preparing files for %s: %w", name, err)
	}

	return runContainer(
		ctx, c.vtgateImage(),
		testcontainers.WithCmd(args...),
		testcontainers.WithExposedPorts(
			fmt.Sprintf("%d/tcp", vtgateHTTPPort),
			fmt.Sprintf("%d/tcp", vtgateGRPCPort),
			fmt.Sprintf("%d/tcp", vtgateMySQLPort),
		),
		network.WithNetwork([]string{name}, c.network),
		// Docker on Linux does not resolve host.docker.internal without an
		// explicit host-gateway mapping. Tests use it to point vtgate at
		// collectors listening on the host.
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.ExtraHosts = append(hc.ExtraHosts, "host.docker.internal:host-gateway")
		}),
		testcontainers.WithEnv(mergeEnv(map[string]string{"VTTEST": "endtoend"}, c.opts.vtgateEnv)),
		filesOpt,
		testcontainers.WithLogConsumers(c.newFileLogConsumer(t, name)),
		testcontainers.WithWaitStrategyAndDeadline(
			defaultStartupTimeout,
			wait.ForHTTP("/debug/vars").
				WithPort(fmt.Sprintf("%d/tcp", vtgateHTTPPort)).
				WithStartupTimeout(defaultStartupTimeout).
				WithPollInterval(defaultPollInterval),
		),
	)
}

// argsContain reports whether any arg mentions the given flag name.
func argsContain(args []string, flag string) bool {
	for _, arg := range args {
		if strings.Contains(arg, flag) {
			return true
		}
	}
	return false
}
