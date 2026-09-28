# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# esbuild runs on the build platform; only the server is cross-compiled.
RUN make web && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /k8sfoams .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /k8sfoams /k8sfoams
EXPOSE 8080
ENTRYPOINT ["/k8sfoams"]
