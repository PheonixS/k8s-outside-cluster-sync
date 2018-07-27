/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Note: the example only works with the code within the same release/branch.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/ini.v1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

type Element struct {
	Key   string
	Value string
}

type Conf struct {
	element []Element
}

var CONNECTORS int = 16

var Confmap map[string]Conf
var configurationFile = "daemon.ini"
var cfg = ini.Empty()

func processAddToConfiguration(applicationServerHostname string, applicationServerIP string, applicationServerPort int) error {

	for i := 1; i <= CONNECTORS; i++ {
		var lvsname = fmt.Sprintf("%s%d", "lvs", i)
		cfg.Section(lvsname).Key(applicationServerHostname).SetValue(fmt.Sprintf("%s:%d", applicationServerIP, applicationServerPort))
	}

	cfg.SaveTo(configurationFile)
	return nil
}

func processVerifyConfiguration(applicationServerHostname string, applicationServerIP string, applicationServerPort int) error {
	for i := 1; i <= CONNECTORS; i++ {
		var lvsname = fmt.Sprintf("%s%d", "lvs", i)
		cfg.Section(lvsname).DeleteKey(applicationServerIP)
	}

	cfg.SaveTo(configurationFile)
	return nil
}

func processDeleteFromConfiguration(applicationServerHostname string) error {
	for i := 1; i <= CONNECTORS; i++ {
		var lvsname = fmt.Sprintf("%s%d", "lvs", i)
		cfg.Section(lvsname).DeleteKey(applicationServerHostname)
	}

	cfg.SaveTo(configurationFile)
	return nil
}

func main() {
	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// Examples for error handling:
	// - Use helper functions like e.g. errors.IsNotFound()
	// - And/or cast to StatusError and use its properties like e.g. ErrStatus.Message
	namespace := "default"
	svc := "test-nginx"
	var filter = fmt.Sprintf("k8s-app=%s", svc)

	//verify configurationFile
	pods, err := clientset.CoreV1().Pods("").List(metav1.ListOptions{LabelSelector: filter})
	if err != nil {
		panic(err.Error())
	}
	fmt.Printf("There are %d pods in the cluster\n Syncronising configuration...\n", len(pods.Items))

	for _, val := range pods.Items {
		processAddToConfiguration(val.Name, val.Status.PodIP, 8080)
	}

	watcher, err := clientset.Core().Pods(namespace).Watch(metav1.ListOptions{LabelSelector: filter})
	if err != nil {
		fmt.Printf("Nothing found with k8s-app=test-nginx")
	}

	ch := watcher.ResultChan()

	for event := range ch {
		pod, ok := event.Object.(*v1.Pod)
		if !ok {
			log.Fatal("unexpected type")
		}

		fmt.Printf("EVENT: %s Name: %s IP: %s PHASE: %s\n", event.Type, pod.Name, pod.Status.PodIP, pod.Status.Phase)

		switch event.Type {
		case watch.Added:
			if pod.Status.Phase == "Running" {
				processAddToConfiguration(pod.Name, pod.Status.PodIP, 8080)
			}
		case watch.Modified:
			if pod.Status.Phase == "Running" {
				processAddToConfiguration(pod.Name, pod.Status.PodIP, 8080)
			}
		case watch.Deleted:
			processDeleteFromConfiguration(pod.Name)
		}
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
