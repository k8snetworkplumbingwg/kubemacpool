ARG GO_VERSION
FROM golang:${GO_VERSION}
 
RUN apt-get clean && apt-get update && apt-get install -y unzip && apt-get upgrade -y

RUN go get golang.org/x/tools/go/packages
RUN go get github.com/golang/mock/gomock
RUN go install github.com/golang/mock/mockgen

RUN go get golang.org/x/tools/cmd/cover
RUN go get github.com/mattn/goveralls

RUN export KUSTOMIZE_DIR=/opt/kustomize \
    && mkdir -p $KUSTOMIZE_DIR \
    && cd $KUSTOMIZE_DIR \
    && wget "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv3.5.4/kustomize_v3.5.4_linux_amd64.tar.gz" \
    && tar xzf ./kustomize_v3.5.4_linux_amd64.tar.gz \
    && rm kustomize_v3.5.4_linux_amd64.tar.gz \
    && ln -s $KUSTOMIZE_DIR/kustomize /usr/bin/kustomize

WORKDIR /
 
COPY install.sh /install.sh
COPY entrypoint.sh /entrypoint.sh
 
RUN /install.sh
