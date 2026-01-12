# Cluster E2E CI Simplification Spec

## Overview

Replace 61 generated workflow files with a single unified workflow using gotestsum ci-matrix for automatic test partitioning.

## Files

### `test/deps.yaml`

Maps packages to their special dependencies. Packages not listed use default setup (MySQL 8.4, etcd).

```yaml
vitess.io/vitess/go/test/endtoend/backup/xtrabackup:
  - xtrabackup

vitess.io/vitess/go/test/endtoend/backup/xtrabackupstream:
  - xtrabackup

# ... etc
```

### `.github/workflows/cluster_endtoend.yml`

```yaml
name: Cluster E2E Tests

on:
  push:
    branches:
      - main
      - "release-[0-9]+.[0-9]"
  pull_request:

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

permissions: read-all

jobs:
  changes:
    runs-on: ubuntu-24.04
    outputs:
      should_run: ${{ steps.changes.outputs.end_to_end }}
    steps:
      - uses: actions/checkout@v6
      - uses: dorny/paths-filter@v3
        id: changes
        with:
          filters: |
            end_to_end:
              - 'go/**/*.go'
              - 'go/vt/sidecardb/**/*.sql'
              - 'test/**'
              - 'Makefile'
              - 'build.env'
              - 'go.mod'
              - 'go.sum'
              - 'proto/*.proto'
              - 'tools/**'
              - 'config/**'
              - 'bootstrap.sh'

  matrix:
    needs: changes
    if: needs.changes.outputs.should_run == 'true'
    runs-on: ubuntu-24.04
    outputs:
      matrix: ${{ steps.generate.outputs.matrix }}
    steps:
      - uses: actions/checkout@v6
      
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      
      - name: Generate test matrix
        id: generate
        run: |
          go list ./go/test/endtoend/... | go tool gotestsum tool ci-matrix --partitions 16 > matrix.json
          echo "matrix=$(jq -c . matrix.json)" >> $GITHUB_OUTPUT

  test:
    needs: [changes, matrix]
    if: needs.changes.outputs.should_run == 'true'
    runs-on: ubuntu-24.04
    timeout-minutes: 60
    strategy:
      fail-fast: false
      matrix: ${{ fromJSON(needs.matrix.outputs.matrix) }}
    env:
      VTDATAROOT: /tmp/
    
    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - uses: ./.github/actions/tune-os

      - name: Determine dependencies
        id: deps
        run: |
          deps=""
          for pkg in ${{ matrix.packages }}; do
            pkg_deps=$(yq -r ".\"$pkg\" // [] | .[]" test/deps.yaml 2>/dev/null)
            deps="$deps $pkg_deps"
          done
          deps=$(echo $deps | tr ' ' '\n' | sort -u | xargs)
          echo "deps=$deps" >> $GITHUB_OUTPUT

      - name: Setup MySQL
        if: ${{ !contains(steps.deps.outputs.deps, 'xtrabackup') }}
        uses: ./.github/actions/setup-mysql
        with:
          flavor: mysql-8.4

      - name: Install XtraBackup
        if: contains(steps.deps.outputs.deps, 'xtrabackup')
        run: |
          sudo apt-get -qq update
          sudo apt-get -qq install -y lsb-release gnupg2
          wget https://repo.percona.com/apt/percona-release_latest.$(lsb_release -sc)_all.deb
          sudo DEBIAN_FRONTEND="noninteractive" dpkg -i percona-release_latest.$(lsb_release -sc)_all.deb
          sudo percona-release setup ps80
          sudo apt-get -qq update
          sudo apt-get -qq install -y percona-server-server percona-server-client percona-xtrabackup-80 lz4
          sudo service mysql stop
          sudo ln -s /etc/apparmor.d/usr.sbin.mysqld /etc/apparmor.d/disable/
          sudo apparmor_parser -R /etc/apparmor.d/usr.sbin.mysqld

      - name: Install Consul/Zookeeper
        if: contains(steps.deps.outputs.deps, 'consul') || contains(steps.deps.outputs.deps, 'zookeeper')
        run: make tools

      - name: Install Minio
        if: contains(steps.deps.outputs.deps, 'minio')
        run: |
          wget https://dl.min.io/server/minio/release/linux-amd64/minio
          chmod +x minio
          sudo mv minio /usr/local/bin

      - name: Install base dependencies
        run: |
          sudo apt-get -qq update
          sudo apt-get -qq install -y make unzip g++ etcd-client etcd-server curl git wget mysql-shell
          sudo service etcd stop
          go mod download

      - name: Build
        run: make build

      - name: Run tests
        run: |
          source build.env
          go tool gotestsum \
            --format github-actions \
            --junitfile report.xml \
            --rerun-fails=3 \
            --rerun-fails-max-failures=10 \
            -- -v -timeout 45m ${{ matrix.packages }}

      - name: Test Summary
        if: always()
        uses: test-summary/action@v2
        with:
          paths: report.xml
          show: fail
```

## Dependency Types

| Dep | What it installs | Replaces default MySQL? |
|-----|------------------|------------------------|
| `xtrabackup` | Percona Server + XtraBackup | Yes |
| `consul` | Consul via `make tools` | No |
| `zookeeper` | Zookeeper via `make tools` | No |
| `minio` | Minio S3 server | No |

## TODO / Deferred Items

### Launchable Integration
The old system has Launchable test analytics integration. To be implemented later:
```yaml
- name: Setup launchable dependencies
  if: github.event_name == 'pull_request' && github.event.pull_request.draft == 'false' && github.base_ref == 'main'
  run: |
    pip3 install --user launchable~=1.0 > /dev/null
    launchable verify || true
    launchable record build --name "$GITHUB_RUN_ID" --no-commit-collection --source .

- name: Record test results in launchable
  if: # after tests, on PRs to main
  run: launchable record tests --build "$GITHUB_RUN_ID" go-test . || true
```

### Build Tags
`vtgate_transaction` tests require build tag `debug2PC`. Need to handle:
- Add to deps.yaml as a special dep type, or
- Build with tag globally (if safe)

### MySQL Tuning (LimitResourceUsage)
For `vreplication*` and `*heavy` tests, the old system applies MySQL tuning:
```
innodb_buffer_pool_size=64M
innodb_doublewrite=OFF
innodb_flush_log_at_trx_commit=0
performance_schema=OFF
# ... etc
```
Consider applying globally or as a dep.

### MySQL Binlog Features
For `vrepl` tests, enables:
- `binlog-transaction-compression=ON` - tests vreplication with compressed binlog
- `binlog-row-value-options=PARTIAL_JSON` - tests vreplication with partial JSON logging

### Memory Check
`vtorc` tests check for 15GB+ RAM. May need separate handling on standard runners.

### mysql-shell
Non-xtrabackup tests install `mysql-shell`. Add to base dependencies.

## Migration

1. Create `test/deps.yaml` (done)
2. Create `.github/workflows/cluster_endtoend.yml`
3. Test on a branch
4. Delete old `cluster_endtoend_*.yml` files (61 files)
5. Optionally remove `test/ci_workflow_gen.go` and templates

## Comparison

| Aspect | Before | After |
|--------|--------|-------|
| Workflow files | 61 | 1 |
| Config lines | ~1600 (config.json) | ~25 (deps.yaml) |
| Code generation | Required | None |
| Adding a test | Edit config + regenerate | Just add test file |
| Test balancing | Manual shard assignment | Automatic via timing |
