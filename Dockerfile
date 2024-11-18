ARG KINE_VERSION="v0.13.2"
FROM rancher/kine:${KINE_VERSION} as kine

# Build program
FROM golang:1.23 as builder

WORKDIR /vcluster-dev
ARG TARGETOS
ARG TARGETARCH
ARG BUILD_VERSION=dev
ARG TELEMETRY_PRIVATE_KEY=""
ARG HELM_VERSION="v3.16.2"
ARG CONTAINERD_VERSION="1.7.23"
ARG RUNC_VERSION="v1.1.15"
ARG KUBERNETES_VERSION="v1.31.1"
ARG FLANNELD_VERSION="v0.26.1"
ARG FLANNEL_VERSION="v1.6.0-flannel1"
ARG CNI_PLUGINS="v1.6.0"

# Install helm binary
RUN curl -s https://get.helm.sh/helm-${HELM_VERSION}-linux-${TARGETARCH}.tar.gz > helm3.tar.gz && tar -zxvf helm3.tar.gz linux-${TARGETARCH}/helm && chmod +x linux-${TARGETARCH}/helm && mv linux-${TARGETARCH}/helm /usr/local/bin/helm && rm helm3.tar.gz && rm -R linux-${TARGETARCH}

# Install flanneld
RUN curl -L -o flanneld.tgz https://github.com/flannel-io/flannel/releases/download/${FLANNELD_VERSION}/flannel-${FLANNELD_VERSION}-linux-${TARGETARCH}.tar.gz && \
    mkdir flanneld && tar -zxvf flanneld.tgz -C flanneld && \
    mv flanneld/flanneld /usr/local/bin && \
    rm flanneld.tgz && rm -rf flanneld

# Install flannel
RUN curl -L -o flannel.tgz https://github.com/flannel-io/cni-plugin/releases/download/${FLANNEL_VERSION}/cni-plugin-flannel-linux-${TARGETARCH}-${FLANNEL_VERSION}.tgz && \
    tar -zxvf flannel.tgz && \
    mkdir -p /opt/cni/bin && \
    mv flannel-${TARGETARCH} /opt/cni/bin/flannel && \
    rm flannel.tgz

# Install cni plugins
RUN curl -L -o cni.tgz https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS}/cni-plugins-linux-${TARGETARCH}-${CNI_PLUGINS}.tgz && \
    mkdir cni && tar -zxvf cni.tgz -C cni && \
    mv cni/loopback /opt/cni/bin && \
    mv cni/portmap /opt/cni/bin && \
    mv cni/bandwidth /opt/cni/bin && \
    mv cni/bridge /opt/cni/bin && \
    mv cni/firewall /opt/cni/bin && \
    mv cni/host-local /opt/cni/bin && \
    rm cni.tgz && rm -rf cni

# Install containerd
RUN curl -L -o containerd.tgz https://github.com/containerd/containerd/releases/download/v${CONTAINERD_VERSION}/containerd-${CONTAINERD_VERSION}-linux-${TARGETARCH}.tar.gz && \
    tar -zxvf containerd.tgz bin && \
	chmod +x bin/containerd-shim-runc-v2 && mv bin/containerd-shim-runc-v2 /usr/local/bin && \
	chmod +x bin/containerd && mv bin/containerd /usr/local/bin && \
	chmod +x bin/ctr && mv bin/ctr /usr/local/bin && \
	rm containerd.tgz && rm -rf bin

# Install runc
RUN curl -L -o runc https://github.com/opencontainers/runc/releases/download/${RUNC_VERSION}/runc.${TARGETARCH} && \
	chmod +x runc && mv runc /usr/local/bin

# Install kubernetes
RUN curl -L -o kubelet https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kubelet && \
    curl -L -o kube-proxy https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kube-proxy && \
    curl -L -o kubeadm https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kubeadm && \
    curl -L -o kubectl https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kubectl && \
    curl -L -o kube-apiserver https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kube-apiserver && \
    curl -L -o kube-controller-manager https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kube-controller-manager && \
    curl -L -o kube-scheduler https://dl.k8s.io/${KUBERNETES_VERSION}/bin/linux/${TARGETARCH}/kube-scheduler && \
	chmod +x kube-scheduler && mv kube-scheduler /usr/local/bin && \
	chmod +x kube-controller-manager && mv kube-controller-manager /usr/local/bin && \
	chmod +x kube-apiserver && mv kube-apiserver /usr/local/bin && \
	chmod +x kubectl && mv kubectl /usr/local/bin && \
	chmod +x kubelet && mv kubelet /usr/local/bin && \
	chmod +x kubeadm && mv kubeadm /usr/local/bin && \
	chmod +x kube-proxy && mv kube-proxy /usr/local/bin

# Install Delve for debugging
RUN if [ "${TARGETARCH}" = "amd64" ] || [ "${TARGETARCH}" = "arm64" ]; then go install github.com/go-delve/delve/cmd/dlv@latest; fi

# Install iptables for kube-proxy
RUN apt update && apt install -y iptables

# Install kine
COPY --from=kine /bin/kine /usr/local/bin/kine

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY vendor/ vendor/

# Copy the go source
COPY cmd/vcluster cmd/vcluster
COPY cmd/vclusterctl cmd/vclusterctl
COPY pkg/ pkg/
COPY config/ config/

ENV GO111MODULE on
ENV DEBUG true

# create and set GOCACHE now, this should slightly speed up the first build inside of the container
# also create /.config folder for GOENV, as dlv needs to write there when starting debugging
RUN mkdir -p /.cache /.config
ENV GOCACHE=/.cache
ENV GOENV=/.config

# Set home to "/" in order to for kubectl to automatically pick up vcluster kube config
ENV HOME /

# Build cmd
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
	--mount=type=cache,id=gobuild,target=/.cache/go-build \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GO111MODULE=on go build -mod vendor -ldflags "-X github.com/loft-sh/vcluster/pkg/telemetry.SyncerVersion=$BUILD_VERSION -X github.com/loft-sh/vcluster/pkg/telemetry.telemetryPrivateKey=$TELEMETRY_PRIVATE_KEY" -o /vcluster cmd/vcluster/main.go

# RUN useradd -u 12345 nonroot
# USER nonroot

ENTRYPOINT ["go", "run", "-mod", "vendor", "cmd/vcluster/main.go", "start"]

# we use alpine for easier debugging
FROM alpine:3.20

# install runtime dependencies
RUN apk add --no-cache ca-certificates zstd tzdata

# Set root path as working directory
WORKDIR /

COPY --from=kine /bin/kine /usr/local/bin/kine
COPY --from=builder /vcluster .
COPY --from=builder /usr/local/bin/helm /usr/local/bin/helm

# RUN useradd -u 12345 nonroot
# USER nonroot

ENTRYPOINT ["/vcluster", "start"]
