#!/bin/bash

docker run --rm -t --volume `pwd`:/go/src/github.com/k8s-nativelb  --volume $HOME/.kube/:/root/.kube/ --volume $HOME/.minikube:/home/travis/.minikube --env KUBECONFIG=/root/.kube/config --workdir /go/src/github.com/k8s-nativelb/ golang:1.11.0 make test
