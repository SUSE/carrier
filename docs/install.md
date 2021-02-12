# Installation

- [Installation](#installation)
  - [Provision of External IP for LoadBalancer service type in Kubernetes](#provision-of-external-ip-for-loadbalancer-service-type-in-kubernetes)
    - [K3s/K3d](#k3sk3d)
    - [Minikube](#minikube)
    - [Kind](#kind)
    - [MircoK8s](#mircok8s)

## Provision of External IP for LoadBalancer service type in Kubernetes

Local kubernetes platforms do not have the ability to provide external IP address when you create a kubernetes service with `LoadBalancer` service type. The following steps will enable this ability for different local kubernetes platforms. Follow these steps before installing carrier.

### K3s/K3d

* Provision of LoadBalancer IP is enabled by default in K3s/K3d.

### Minikube

* Install and configure MetalLB
```
minikube addons enable metallb

MINIKUBE_IP=($(minikube ip))
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: metallb-system
  name: config
data:
  config: |
    address-pools:
    - name: default
      protocol: layer2
      addresses:
      - ${MINIKUBE_IP}/28
EOF
```

### Kind 

* Install JQ from https://stedolan.github.io/jq/download/

* Install and configure MetalLB 
```
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.9.5/manifests/namespace.yaml
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.9.5/manifests/metallb.yaml
kubectl create secret generic -n metallb-system memberlist --from-literal=secretkey="$(openssl rand -base64 128)"

SUBNET_IP=`docker network inspect kind | jq -r '.[0].IPAM.Config[0].Subnet'`
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: metallb-system
  name: config
data:
  config: |
    address-pools:
    - name: default
      protocol: layer2
      addresses:
      - ${SUBNET_IP}/28
EOF
```

### MircoK8s

* Install and configure MetalLB
```
INTERFACE=`ip route | grep default | awk '{print $5}'`
IP=`ifconfig $INTERFACE | sed -n '2 p' | awk '{print $2}'`
microk8s enable metallb:${IP}/16
```


