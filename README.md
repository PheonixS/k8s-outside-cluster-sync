### What is it?
This simple daemon allow you to sync TCP enabled application inside in Kubernetes cluster with external LVS servers.  
This is usefull only for on-premise installation of Kubernetes, then you cannot use load balancers. Like ELB.  

![Diagram](scheme.png)

Application create watch on update/modify/deletes of pods and update local configuration of lvs servers.  
It change weights for real serves from 0 to 100 with step 20. Sleep time is configurable.  
You need to run this daemon on **every** LVS server.  

### Configuration
Most important part is:  
`.spec.template.metadata.labels.progress` - initial status of progress for pod newly created. Default is 0. **This option is very important!**  
`.spec.progressDeadlineSeconds` - deployment will be marked as failed if pod unable to complete startup after this time  
`.spec.minReadySeconds` - we define minimum avaliable time to bring up next pod  

#### Initial state of deployment
```yaml
apiVersion: v1
items:
- apiVersion: extensions/v1beta1
  kind: Deployment
  metadata:
    annotations:
      deployment.kubernetes.io/revision: "2"
    labels:
      k8s-app: nginx-test
    name: nginx-test
    namespace: default
  spec:
    minReadySeconds: 600
    progressDeadlineSeconds: 1200
    replicas: 5
    revisionHistoryLimit: 10
    selector:
      matchLabels:
        k8s-app: nginx-test
    strategy:
      rollingUpdate:
        maxSurge: 1
        maxUnavailable: 1
      type: RollingUpdate
    template:
      metadata:
        creationTimestamp: null
        labels:
          k8s-app: nginx-test
          progress: "0"
        name: nginx-test
      spec:
        containers:
        - image: nginx
          imagePullPolicy: Always
          name: nginx-test
          resources: {}
          securityContext:
            privileged: false
          terminationMessagePath: /dev/termination-log
          terminationMessagePolicy: File
        dnsPolicy: ClusterFirst
        restartPolicy: Always
        schedulerName: default-scheduler
        securityContext: {}
        terminationGracePeriodSeconds: 30
```

Configuration is stored in ini file `config.ini`  
Avaliable options:  
``lvsConfigFilePath`` - absolute path to lvs configuration  
``labelToMonitorName`` - label of pods what you need to monitor (Example: `k8s-app=nginx-test`)  
``labelToMonitorNamespace`` - namespace for this pod  
``lvsDestionationPort`` - port of real server where you will be forward traffic  
``lvsSleepTime`` - sleep time for every increment of weights  

Before running this application you need to adjust this params:  
lvsConfig:  
`virtual` field need to be changed to you external ip.  

### Installation
This GO script use dependecies defined in `Godeps` dir.  
Install the dependencies:  

```sh
$ cd k8s-outside-cluster-sync
$ godep load ./...
$ go run main.go
```
