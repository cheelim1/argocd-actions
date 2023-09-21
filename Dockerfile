ARG GO_VERSION=1.18-alpine3.15
ARG FROM_IMAGE=alpine:3.15

# Builder stage
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION} AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION

LABEL org.opencontainers.image.source="https://github.com/cheelim1/argocd-actions"

WORKDIR /app

COPY ./ /app

RUN apk update && \
    apk add --no-cache ca-certificates gettext git make curl unzip && \
    rm -rf /tmp/* && \
    rm -rf /var/cache/apk/* && \
    rm -rf /var/tmp/* && \
    make build TARGETOS=$TARGETOS TARGETARCH=$TARGETARCH VERSION=$VERSION

# Final stage
FROM ${FROM_IMAGE}

# Ensure ca-certificates are present in the final image
RUN apk add --no-cache ca-certificates

# Copy the built binary from the builder stage
COPY --from=builder /app/dist/argocd-actions /bin/argocd-actions

# Set the entry point
ENTRYPOINT ["argocd-actions"]