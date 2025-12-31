# Copyright 2025 The Vitess Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG GO_VERSION=1.25.3
ARG MYSQL_VERSION=8.4.4

# Build stage
FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /vt/src/vitess.io/vitess
COPY . .
RUN NOVTADMINBUILD=1 make install PREFIX=/vt/install

# Runtime stage
FROM mysql:${MYSQL_VERSION}

# Create vitess user
RUN groupadd -r vitess && useradd -r -g vitess vitess

# Copy Vitess binaries and config
COPY --link --from=builder --chown=vitess:vitess /vt/install /vt
COPY --link --from=builder /vt/src/vitess.io/vitess/config/init_db.sql /vt/config/
COPY --link --from=builder /vt/src/vitess.io/vitess/config/mycnf /vt/config/

# Environment for Vitess binaries
ENV VTROOT=/vt
ENV VTDATAROOT=/vt/vtdataroot
ENV PATH=/vt/bin:$PATH

# Create data directory with vitess ownership
RUN mkdir -p /vt/vtdataroot && chown vitess:vitess /vt/vtdataroot

USER vitess
