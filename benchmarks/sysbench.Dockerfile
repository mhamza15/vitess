FROM ubuntu:22.04 AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    make \
    automake \
    autoconf \
    libtool \
    pkg-config \
    gcc \
    g++ \
    libmysqlclient-dev \
    libssl-dev \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 https://github.com/akopytov/sysbench.git /src/sysbench && \
    cd /src/sysbench && \
    ./autogen.sh && \
    ./configure --with-mysql && \
    make -j$(nproc) && \
    make install

FROM ubuntu:22.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    libmysqlclient21 \
    libssl3 \
    mysql-client \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/bin/sysbench /usr/local/bin/sysbench
COPY --from=builder /usr/local/share/sysbench /usr/local/share/sysbench

RUN git clone --depth 1 https://github.com/Percona-Lab/sysbench-tpcc.git /src/sysbench-tpcc

COPY sysbench/entrypoint.sh /entrypoint.sh
COPY sysbench/olap_sort.lua /usr/local/share/sysbench/olap_sort.lua
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
