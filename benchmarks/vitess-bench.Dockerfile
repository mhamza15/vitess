FROM vitess/lite AS base

USER root

RUN apt-get update && apt-get install -y --no-install-recommends curl jq \
    && rm -rf /var/lib/apt/lists/*

USER vitess
