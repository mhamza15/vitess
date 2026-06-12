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

package cli

// Registers the fastquery server when VT_FASTQUERY is set. It listens
// on grpc-port+1 by convention; the client side (grpctabletconn) uses
// the same convention, also gated by VT_FASTQUERY.

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"vitess.io/vitess/go/vt/log"
	"vitess.io/vitess/go/vt/servenv"
	"vitess.io/vitess/go/vt/vttablet/fastquery"
	"vitess.io/vitess/go/vt/vttablet/tabletserver"
)

func init() {
	tabletserver.RegisterFunctions = append(tabletserver.RegisterFunctions, func(qsc tabletserver.Controller) {
		if os.Getenv("VT_FASTQUERY") == "" {
			return
		}
		port := servenv.GRPCPort() + 1
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Error("fastquery: failed to listen, transport disabled", slog.Int("port", port), slog.Any("error", err))
			return
		}
		log.Info("fastquery: listening", slog.Int("port", port))
		go fastquery.Serve(l, qsc.QueryService())
	})
}
