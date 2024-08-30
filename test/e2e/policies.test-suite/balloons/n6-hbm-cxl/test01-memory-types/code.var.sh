helm-terminate
helm_config=${TEST_DIR}/balloons-memory-types.cfg helm-launch balloons

cleanup() {
    vm-command "kubectl delete pods -n kube-system pod0; kubectl delete pods --all --now"
    return 0
}

cleanup

# pod0: run on reserved CPUs
POD_ANNOTATION="balloon.balloons.resource-policy.nri.io/container.pod0c0: two-cpu"
CPUREQ="1" MEMLIM="1500M" CPULIM="" MEMREQ="" CONTCOUNT=2 create balloons-busybox
report allowed

echo "TODO: create pods and validate memory allocations with various memory type requests"
breakpoint

cleanup

helm-terminate
