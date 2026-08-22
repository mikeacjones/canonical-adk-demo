# Alternate Go Google ADK worker. Demo cloud runs the Python worker by default;
# this image implements the identical task-queue and gateway contract.
FROM golang:1.25-bookworm AS build

WORKDIR /src/go
COPY go/go.mod go/go.sum ./
RUN go mod download
COPY go/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/travel-worker-go ./cmd/worker

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app/go
COPY --from=build /out/travel-worker-go /app/go/travel-worker-go
USER nonroot:nonroot
ENTRYPOINT ["/app/go/travel-worker-go"]
