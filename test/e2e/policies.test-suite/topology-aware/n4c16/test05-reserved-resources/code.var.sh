# Test that
# - kube-system containers are pinned on Reserved CPUs.
# - Reserved CPU allocation and releasing works.
# - A pod cannot be launched if reserved CPU capacity in insufficient.

AVAILABLE_CPU="cpuset:4-7,8-13"

# This script will create pods to the kube-system namespace
# that is not automatically cleaned up by the framework.
# Make sure the namespace is clear when starting the test and clean it up
# if exiting with success. Otherwise leave the pod running for
# debugging in case of a failure.
cleanup-kube-system() {
    ( vm-command "kubectl delete pods pod0 pod1 pod2 pod3 pod4 pod5 -n kube-system --now --ignore-not-found=true" ) || true
}
cleanup-kube-system

# Test launch failure, Reserved CPUs is not subset of Available CPUs
helm-terminate
RESERVED_CPU="cpuset:3,7,11,15"
helm_config=$(instantiate helm-config.yaml)
( launch_timeout=5s helm-launch topology-aware ) && error "unexpected success" || {
    echo "Launch failed as expected"
    get-config-node-status-error topologyawarepolicies/default || :
}

# Test launch failure, there are more reserved CPUs than available CPUs
helm-terminate
RESERVED_CPU='"11"'
helm_config=$(instantiate helm-config.yaml)
( launch_timeout=5s helm-launch topology-aware ) && error "unexpected success" || {
    echo "Launch failed as expected"
    get-config-node-status-error topologyawarepolicies/default || :
}

# Test that BestEffort containers are allowed to run on both Reserved
# CPUs when the CPUs are on the same NUMA node.
helm-terminate
RESERVED_CPU="cpuset:10-11"
helm_config=$(instantiate helm-config.yaml) helm-launch topology-aware

namespace=kube-system CONTCOUNT=3 create besteffort
report allowed
verify "cpus['pod0c0'] == cpus['pod0c1'] == cpus['pod0c2'] == {'cpu10', 'cpu11'}"
vm-command "kubectl delete -n kube-system pods pod0"

# Test that BestEffort containers are pinned to reserved CPUs.
helm-terminate
RESERVED_CPU="cpuset:7,11"
helm_config=$(instantiate helm-config.yaml) helm-launch topology-aware

namespace=kube-system CONTCOUNT=4 create besteffort
report allowed
verify "cpus['pod1c0'] == cpus['pod1c1'] == cpus['pod1c2'] == cpus['pod1c3']" \
       "cpus['pod1c0'] == {'cpu07', 'cpu11'}"

# Test that guaranteed kube-system pods are pinned to Reserved CPUs.
namespace=kube-system CPU=200m CONTCOUNT=4 create guaranteed
report allowed
verify "cpus['pod2c0'] == cpus['pod2c1'] == cpus['pod2c2'] == cpus['pod2c3']" \
       "cpus['pod2c0'] == {'cpu07', 'cpu11'}"

# Test requesting more reserved CPUs than available on single node
# but what fits in the node tree.
# pod2 already consumed 4 * 200m of reserved CPUs that have been balanced
# so that at least 200m from both nodes have been consumed. There are
# at most 800m reserved CPUs free on both nodes. Root node still has
# 1200m free. That is, 1000m requesting, isolated-looking guaranteed
# pod should fit in because reserved CPUs are not isolated.
#
# Run this twice to make sure allocated reserved CPUs are released correctly.
for pod in pod3 pod4; do
    namespace=kube-system CPU=100m CONTCOUNT=1 create guaranteed
    report allowed
    verify "cpus['${pod}c0'] == {'cpu07', 'cpu11'}"
    vm-command "kubectl delete -n kube-system pods/$pod --now"
done

# Test requesting more reserved CPUs than available in the system.
# pod5 is expected to run on shared CPUs.
namespace=kube-system CPU=2 CONTCOUNT=1 create guaranteed
report allowed
verify "cpus['pod5c0'] == {'cpu04', 'cpu05', 'cpu06', 'cpu08', 'cpu09', 'cpu10', 'cpu12', 'cpu13'}"

cleanup-kube-system

# Test that the first available CPUs are reserved when reserving milli CPUs.
# The number of reserved CPUs is the ceiling of the milli CPUs.
reset counters
helm-terminate
RESERVED_CPU="2250m"
helm_config=$(instantiate helm-config.yaml) helm-launch topology-aware
namespace=kube-system CPU=2 CONTCOUNT=1 create besteffort
verify "cpus['pod0c0'] == {'cpu04', 'cpu05', 'cpu06'}"

vm-command "kubectl delete -n kube-system pods/pod0"

helm-terminate
