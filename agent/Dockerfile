# Build context is the repo root (needs proto/ + agent/ for the replace).
FROM golang:1.26 AS build
ENV GOWORK=off CGO_ENABLED=0
WORKDIR /src
COPY proto/ ./proto/
COPY agent/ ./agent/
WORKDIR /src/agent
RUN go build -trimpath -o /out/yusui-agent ./cmd/yusui-agent

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends nftables iproute2 ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/yusui-agent /usr/local/bin/yusui-agent
# Requires --cap-add=NET_ADMIN (+ a forwarding netns) to program nftables.
ENTRYPOINT ["/usr/local/bin/yusui-agent"]
