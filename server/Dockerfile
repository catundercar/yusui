# Build the server module standalone (workspace off; only server/ in context).
FROM golang:1.26 AS build
ENV GOWORK=off CGO_ENABLED=0
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN go build -trimpath -o /out/yusui-server ./cmd/yusui-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/yusui-server /yusui-server
ENTRYPOINT ["/yusui-server"]
CMD ["serve"]
