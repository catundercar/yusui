# Build the server with the workspace off. The server module has a local
# `replace github.com/catundercar/yusui/proto => ../proto`, so the proto module
# must sit next to it in the build context (compose builds with context: repo root).
FROM golang:1.26 AS build
ENV GOWORK=off CGO_ENABLED=0
WORKDIR /src
COPY proto/ ./proto/
COPY server/ ./server/
WORKDIR /src/server
RUN go mod download
RUN go build -trimpath -o /out/yusui-server ./cmd/yusui-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/yusui-server /yusui-server
ENTRYPOINT ["/yusui-server"]
CMD ["serve"]
