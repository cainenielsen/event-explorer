# syntax=docker/dockerfile:1

# Both binaries (the API server and the one-shot seeder) come out of this
# same build stage and image so docker-compose can run either from one
# build with a different entrypoint/command.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/seed /usr/local/bin/seed

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
