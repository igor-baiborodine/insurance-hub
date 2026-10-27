ARG BUILDER_IMAGE=docker.io/library/golang:1.27.3-alpine3.23@sha256:0208a74017e3ce41f779876f3c6e9ad5d4030904e8ac6e91b9c5d7711c48362a
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
    -ldflags='-s -w -buildid=' -o /out/product-service ./cmd/server && \
    GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags='-s -w -buildid=' -o /out/product-migrate ./cmd/migrate && \
    GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags='-s -w -buildid=' -o /out/product-seed ./cmd/seed

RUN install -D -m 0444 \
    internal/administration/migrations/00001_product.sql \
    /out/assets/usr/share/insurance-hub/product/migrations/00001_product.sql && \
    install -D -m 0444 \
    internal/administration/seeds/base.json \
    /out/assets/usr/share/insurance-hub/product/catalogs/base.json

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
COPY --from=build --chmod=0555 /out/product-migrate /product-migrate
COPY --from=build --chmod=0555 /out/product-seed /product-seed
COPY --from=build /out/assets/usr/share /usr/share

USER 65532:65532
EXPOSE 8080 8081 9090
ENTRYPOINT ["/product-service"]
