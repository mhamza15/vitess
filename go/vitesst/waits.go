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
	"fmt"
	"strings"
	"time"

	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
	"vitess.io/vitess/go/vt/topo/topoproto"
)

// healthyShardPollInterval is the poll interval for WaitForHealthyShard.
const healthyShardPollInterval = time.Second

// healthcheckPollInterval is the poll interval for WaitForTabletInHealthcheck.
const healthcheckPollInterval = 500 * time.Millisecond

// WaitForHealthyShard polls the shard's topology record until it has a
// primary. It checks the topo record only, not tablet health.
func (c *Cluster) WaitForHealthyShard(ctx context.Context, keyspace, shard string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		record, err := c.Vtctld().Shard(ctx, keyspace, shard)
		lastErr = err
		if err == nil && record.GetShard().GetPrimaryAlias().GetUid() != 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("shard %s/%s did not get a primary within %s: %w", keyspace, shard, timeout, errFirst(lastErr, ctx.Err()))
		case <-time.After(healthyShardPollInterval):
		}
	}
}

// WaitForTabletInHealthcheck polls the vtgate's healthcheck view until it
// reports the tablet with the given type ("primary", "replica" or "rdonly")
// and serving state.
func (vtg *VTGate) WaitForTabletInHealthcheck(ctx context.Context, tablet *Tablet, tabletType string, serving bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := vtg.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	alias := topoproto.TabletAliasString(&topodatapb.TabletAlias{Cell: tablet.Cell, Uid: uint32(tablet.UID)})
	wantType := strings.ToUpper(tabletType)
	wantState := "NOT_SERVING"
	if serving {
		wantState = "SERVING"
	}

	var lastErr error
	for {
		qr, err := conn.ExecuteFetch("show vitess_tablets", 1000, false)
		lastErr = err
		if err == nil {
			for _, row := range qr.Rows {
				if row[5].ToString() == alias && row[3].ToString() == wantType && row[4].ToString() == wantState {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not report tablet %s as %s %s within %s: %w",
				vtg.name, alias, wantType, wantState, timeout, errFirst(lastErr, ctx.Err()))
		case <-time.After(healthcheckPollInterval):
		}
	}
}
