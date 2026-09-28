# Two-stage build for the Tandem relay (Render free tier, or any Docker host).
# Build from the REPO ROOT:  docker build -f app/Dockerfile .
# Render: Root Directory = app, Dockerfile = app/Dockerfile (paths relative to root).

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app ./app
WORKDIR /src/app
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tandem ./cmd/tandem

FROM debian:bookworm-slim
COPY --from=build /out/tandem /usr/local/bin/tandem
EXPOSE 8790
# Render injects $PORT; the relay honours it (falling back to 8790 elsewhere).
CMD ["tandem", "relay"]
