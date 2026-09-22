# Test that the introspection interface describes the node and the
# placement of containers, and that it is not served unless enabled.

helm-terminate

# With introspection disabled, the endpoint must not exist at all.
helm_config=$(instantiate helm-config.yaml) helm-launch topology-aware
verify-introspect-disabled
helm-terminate

helm_config=$(INTROSPECTION=true instantiate helm-config.yaml) helm-launch topology-aware

# The snapshot describes the machine before any workload exists.
verify-introspect-jq '.version == "v1alpha1"'
verify-introspect-jq '.node.policy == "topology-aware"'
verify-introspect-jq '(.hardware.cpus | length) == 16'
verify-introspect-jq '(.hardware.numaNodes | length) == 4'
verify-introspect-jq '(.hardware.cpus | map(.id) | unique | length) == 16'

# The effective configuration travels with it. The concrete configuration
# type is the whole custom resource, so its spec is one level down.
verify-introspect-jq '.config.data.spec.pinCPU == true'
# Note the // "" on every optional string: these fields are omitempty, so
# the failure case is a missing key, and jq's null != "" is true. Without
# the fallback the assertion would pass exactly when it should fail.
verify-introspect-jq '(.config.name // "") != ""'

# A guaranteed pod shows up with its applied cpuset, and the CPUs it got
# are fully committed.
CPU=2 MEM=100M create guaranteed
report allowed

verify-introspect-jq '[.containers[]? | select(.name == "pod0c0")] | length == 1'
verify-introspect-jq '[.containers[]? | select(.name == "pod0c0")][0].cpuRequest == 2000'
verify-introspect-jq '
    ([.containers[]? | select(.name == "pod0c0")][0].applied.cpusetCpus // "") != ""
    '
verify-introspect-jq '
    [.derived.cpuCommitment | to_entries[] | select(.value == 1000)] | length == 2
    ' 15

# The pod's container is gone from the snapshot once it is deleted. Note
# that kubectl runs in the VM: a bare kubectl here would talk to whatever
# cluster the host happens to point at, and --ignore-not-found would make
# that look like success.
vm-command "kubectl delete pods pod0 --now"
verify-introspect-jq '[.containers[]? | select(.name == "pod0c0")] | length == 0'

# A burstable container is tracked too, with its request and the shared
# cpuset it landed on. The variables are the ones burstable.yaml.in reads —
# CPU and MEM are silently ignored by that template.
CPUREQ=1 CPULIM=2 MEMREQ=100M MEMLIM=200M create burstable
report allowed

verify-introspect-jq '
    [.containers[]? | select(.name == "pod1c0")][0] as $c
    | $c.cpuRequest == 1000 and ($c.applied.cpusetCpus // "") != ""
    ' 15

vm-command "kubectl delete pods --all --now"
helm-terminate
