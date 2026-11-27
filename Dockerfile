# syntax=docker/dockerfile:1

# Build a static driftwatch binary.
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/driftwatch ./cmd/driftwatch

# Terraform ships as a static binary, so it runs on distroless too.
FROM hashicorp/terraform:1.17.1 AS terraform

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=terraform /bin/terraform /usr/local/bin/terraform
COPY --from=build /out/driftwatch /usr/local/bin/driftwatch
USER nonroot:nonroot
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/driftwatch"]
CMD ["check", "--dir", "/workspace"]
