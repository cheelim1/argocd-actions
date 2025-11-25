ARG GO_VERSION=1.24-alpine
ARG FROM_IMAGE=alpine:3.20

FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION} AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION

LABEL org.opencontainers.image.source="https://github.com/cheelim1/argocd-actions"

WORKDIR /app

COPY ./ /app

RUN apk update && \
  apk add ca-certificates gettext git make curl unzip && \
  rm -rf /tmp/* && \
  rm -rf /var/cache/apk/* && \
  rm -rf /var/tmp/*

RUN make build TARGETOS=$TARGETOS TARGETARCH=$TARGETARCH VERSION=$VERSION

FROM ${FROM_IMAGE}

COPY --from=builder /app/dist/argocd-actions /bin/argocd-actions

# Install AWS CLI (native Alpine package)
RUN apk add --no-cache aws-cli

ENTRYPOINT ["argocd-actions"]
