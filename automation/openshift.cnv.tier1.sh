#!/bin/bash

set -xe

# Configure kubeconfig
export KUBEVIRT_PROVIDER=external
export KUBECONFIG=${KUBECONFIG:-$HOME/oc4/working/auth/kubeconfig}
export KUBECTL=${KUBECTL:-oc}

# Run workflow tests
focus='test_id:2166|test_id:2167|test_id:2199|test_id:2200|test_id:2164|test_id:2162|test_id:2165|test_id:2179|test_id:2243|test_id:2633|test_id:2995'
make functest E2E_TEST_ARGS="$* --junit-output=junit.functest.xml -ginkgo.noColor -ginkgo.focus $focus"

