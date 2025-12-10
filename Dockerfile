FROM scratch
ARG TARGETPLATFORM
ENTRYPOINT ["/kube-webhook-certgen"]
COPY $TARGETPLATFORM/kube-webhook-certgen /
