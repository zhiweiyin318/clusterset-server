FROM docker.io/openshift/origin-release:golang-1.14 AS builder
WORKDIR /go/src/github.com/open-cluster-management/clusterset-server
COPY . .
ENV GO_PACKAGE github.com/open-cluster-management/clusterset-server

RUN make build --warn-undefined-variables

FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

ENV USER_UID=10001 \
    USER_NAME=acm-foundation

COPY --from=builder /go/src/github.com/open-cluster-management/clusterset-server/server /

RUN microdnf update && \
    microdnf clean all

USER ${USER_UID}