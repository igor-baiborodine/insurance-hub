ARG BUILDER_IMAGE=docker.io/library/golang:1.27.2-alpine3.23@sha256:929e6c7d2f48657a580141155579281939296b952d6426846149fa40a7d090a1
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:2293b36c7c9082bf4115aab724b4d2cddec82c8eba39bf27ac0517e159acf150

FROM ${BUILDER_IMAGE} AS build

ARG SOURCE_REVISION
ARG TARGETARCH
WORKDIR /src

RUN test -n "${SOURCE_REVISION}"
COPY go.mod go.sum ./
RUN GOWORK=off go mod download

COPY cmd ./cmd
COPY gen ./gen
COPY internal ./internal

RUN GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags='-s -w -buildid=' -o /out/product-service ./cmd/server

FROM ${RUNTIME_IMAGE}

ARG SOURCE_CREATED
ARG SOURCE_REPOSITORY
ARG SOURCE_REVISION
ARG SOURCE_VERSION

LABEL org.opencontainers.image.created="${SOURCE_CREATED}" \
      org.opencontainers.image.source="${SOURCE_REPOSITORY}" \
      org.opencontainers.image.revision="${SOURCE_REVISION}" \
      org.opencontainers.image.title="Insurance Hub Product API" \
      org.opencontainers.image.version="${SOURCE_VERSION}"

COPY --from=build --chmod=0555 /out/product-service /product-service

USER 65532:65532
EXPOSE 8080 8081 9090
ENTRYPOINT ["/product-service"]
