FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/code-context ./cmd/code-context

FROM node:24-bookworm AS codegraph
RUN npm install -g @astudioplus/codegraph-mcp@0.20.1 \
    && find /usr/local/lib/node_modules/@astudioplus/codegraph-mcp/bin -name 'codegraph-server-linux-*' -type f | grep -q .

FROM eclipse-temurin:21-jre
RUN apt-get update && apt-get install -y --no-install-recommends git ripgrep curl && rm -rf /var/lib/apt/lists/*
COPY --from=codegraph /usr/local /usr/local
COPY --from=build /out/code-context /usr/local/bin/code-context
EXPOSE 8080
ENTRYPOINT ["code-context"]
