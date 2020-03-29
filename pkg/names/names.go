package names

const MANAGER_NAMESPACE = "kubemacpool-system"

const MANAGER_STATEFULSET = "kubemacpool-mac-controller-manager"

const WEBHOOK_SERVICE = "kubemacpool-service"

const MUTATE_WEBHOOK = "kubemacpool-webhook"

const MUTATE_WEBHOOK_CONFIG = "kubemacpool-mutator"

//TODO delete this const name after removing leader label from namespace
const LEADER_LABEL = "kubemacpool-leader"

const K8S_RUNLABEL = "runlevel"

const OPENSHIFT_RUNLABEL = "openshift.io/run-level"
