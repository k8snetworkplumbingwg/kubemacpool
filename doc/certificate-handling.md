# Kubemacpool webhook certificate handling

Using admission webhooks forces the Kubemacpool application to handle TLS CA and server
certificates. This task is delegated to [kube-admission-webook](https://github.com/qinqon/kube-admission-webhook)
library, which manages certificate rotation using a controller-runtime webhook server and a small cert manager.

To customize how this rotation works the following knobs from the library are 
exposed as environment variables at Kubemacpool pod:

- `CA_ROTATE_INTERVAL`:  Expiration time for CA certificate, default is one year
- `CA_OVERLAP_INTERVAL`:  Duration where expired CA certificate can overlap with new one, in order to allow fluent CA rotation transitioning. defaults to one year
- `CERT_ROTATE_INTERVAL`:  Expiration time for server certificates, default is half a year
