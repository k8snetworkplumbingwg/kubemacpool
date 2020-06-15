#!/usr/bin/env bash

set -xeuo pipefail

keydir="$(mktemp -d)"
kubectl=${kubectl:-cluster/kubectl.sh}
namespace=${namespace:-kubemacpool-system}

generate_keys() {
    local key_dir="$1"
    local expiration_minutes=5

    if !faketime --help > /dev/null 2>&1; then
        sudo dnf install -y fakeclient
    fi

    chmod 0700 "$key_dir"

    pushd $key_dir

    openssl genrsa -out cakey.pem 2048

    # Generate the CA cert and private key
    faketime -f "-$(expr 1440 - $expiration_minutes)m" openssl req -nodes -days 1 -new -x509 -keyout ca.key -out ca.crt -subj "/CN=kubemacpool"

    # Generate the private key for the webhook server
    openssl genrsa -out kubemacpool-service-tls.key 2048

tee csr.conf <<EOF
[ req ]
default_bits = 2048
prompt = no
default_md = sha256
req_extensions = req_ext
distinguished_name = dn

[ dn ]
CN = kubemacpool-service.$namespace.svc

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = kubemacpool-service
DNS.2 = kubemacpool-service.$namespace
DNS.3 = kubemacpool-service.$namespace.svc
DNS.4 = kubemacpool-service.$namespace.svc.cluster
DNS.5 = kubemacpool-service.$namespace.svc.cluster.local

[ v3_ext ]
authorityKeyIdentifier=keyid,issuer:always
basicConstraints=CA:FALSE
keyUsage=keyEncipherment,dataEncipherment
extendedKeyUsage=serverAuth,clientAuth
subjectAltName=@alt_names
EOF

# Generate a Certificate Signing Request (CSR) for the private key, and sign it with the private key of the CA.
    openssl req -new  -key kubemacpool-service-tls.key -config csr.conf -out server.csr
    faketime -f "-$(expr 1440 - $expiration_minutes)m" openssl x509 -req -days 1 -CA ca.crt -CAkey ca.key -CAcreateserial -in server.csr -out kubemacpool-service-tls.crt -extensions v3_ext -extfile csr.conf

    openssl x509 -in ca.crt -text
    openssl x509 -in kubemacpool-service-tls.crt -text

    popd
}


# Generate keys into a temporary directory.
generate_keys "$keydir"

# Create the TLS secret for the generated keys.
$kubectl -n $namespace delete --ignore-not-found secret kubemacpool-service
$kubectl -n $namespace create secret tls kubemacpool-service \
    --cert "${keydir}/kubemacpool-service-tls.crt" \
    --key "${keydir}/kubemacpool-service-tls.key"

# Read the PEM-encoded CA certificate, base64 encode it, and replace the `${CA_PEM_B64}` placeholder in the YAML
# template with it. Then, create the Kubernetes resources.
ca_pem_b64="$(openssl base64 -A <"${keydir}/ca.crt")"
#sed -e 's@${CA_PEM_B64}@'"$ca_pem_b64"'@g' <"${basedir}/deployment.yaml.template" \
#    | kubectl create -f -
$kubectl patch mutatingwebhookconfiguration -n $namespace kubemacpool-mutator  -p '{"webhooks":[{"name":"mutatepods.kubemacpool.io","clientConfig":{"caBundle": "'$ca_pem_b64'"}}]}'
$kubectl patch mutatingwebhookconfiguration -n $namespace kubemacpool-mutator  -p '{"webhooks":[{"name":"mutatevirtualmachines.kubemacpool.io","clientConfig":{"caBundle": "'$ca_pem_b64'"}}]}'

# Restart kubemacpool
$kubectl delete pod -n $namespace --all

#$kubectl wait deployment -n $namespace kubemacpool-mac-controller-manager --for=condition=Available
$kubectl wait pod -n $namespace -l kubemacpool-leader=true --for=condition=Ready

# Delete the key directory to prevent abuse (DO NOT USE THESE KEYS ANYWHERE ELSE).
rm -rf "$keydir"
