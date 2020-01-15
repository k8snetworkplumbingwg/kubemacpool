FROM golang:1.12.12
 
RUN apt-get clean && apt-get update && apt-get install -y unzip && apt-get upgrade -y

RUN go get golang.org/x/tools/go/packages
RUN go get github.com/golang/mock/gomock
RUN go install github.com/golang/mock/mockgen

RUN go get golang.org/x/tools/cmd/cover
RUN go get github.com/mattn/goveralls

RUN GO111MODULE=on go get sigs.k8s.io/kustomize

WORKDIR /
 
COPY install.sh /install.sh
COPY entrypoint.sh /entrypoint.sh
 
RUN /install.sh
