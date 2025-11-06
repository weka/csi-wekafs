# debug

to debug in your IDE:

1. set the KUBECONFIG to the cluster you want to debug
2. run `make deploy-debug` - this will build and deploy the debug image to QUAY repo `csi-wekafs-debug`
3. the script will also deploy the debug image and echo the commands for port-forwarding
4. find the pod you want to debug and `kubectl port-forward <pod-name> 2345:2345 -n $$NAMESPACE"`
5. in your Goland IDE create a "Go Remote" debug to host: localhost port: 2345
6. happy debugging!

