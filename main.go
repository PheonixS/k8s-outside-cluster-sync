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
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	ini "gopkg.in/ini.v1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

type endpoints map[int]string

type realServers struct {
	address string
	port    int
	weight  int
}

type virtual struct {
	hostname  string
	port      int
	protocol  string
	scheduler string
	real      map[int]realServers
}

type virtuals map[string]virtual

var globalConfig = make(virtuals, 0)

type service struct {
	label           string
	namespace       string
	sleepTime       time.Duration
	destinationPort int
}

type configFile struct {
	name string
}

type configuration struct {
	service    service
	configFile configFile
	clientSet  *kubernetes.Clientset
}

var cnf configuration
var vReg = regexp.MustCompile(`(?m)^virtual = (\S*)$`)
var subVirtualReg = regexp.MustCompile(`(?m)^(\S*):(\d+)$`)
var protocolReg = regexp.MustCompile(`(?m)^\s*protocol = (\S*)$`)
var schedulerReg = regexp.MustCompile(`(?m)^\s*scheduler = (\S*)$`)
var realReg = regexp.MustCompile(`(?m)^\s*real = (\S*):(\d+) gate (\d+)$`)
var emptyReg = regexp.MustCompile(`(?m)^$`)

//setup homeDir
func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func check(e error) {
	if e != nil {
		log.Fatal(e.Error())
	}
}

func initK8S() (*kubernetes.Clientset, error) {
	var kubeconfig *string
	if home := HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, err
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}

func initNow() {
	cfg, err := ini.Load("config.ini")
	if err != nil {
		check(err)
	}
	cnf.configFile.name = cfg.Section("main").Key("lvsConfigFilePath").String()

	cnf.service.label = cfg.Section("main").Key("labelToMonitorName").String()
	cnf.service.namespace = cfg.Section("main").Key("labelToMonitorNamespace").String()
	cnf.service.destinationPort, _ = cfg.Section("main").Key("lvsDestionationPort").Int()
	sleepTime, _ := cfg.Section("main").Key("lvsSleepTime").Int()

	cnf.service.sleepTime = time.Duration(sleepTime)
	cnf.clientSet, err = initK8S()
	check(err)
}

func readConfig(path string) ([]string, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	data := strings.Split(string(content), "\n")
	return data, nil
}

func readWholeIni(path string) {
	data, err := readConfig(path)
	check(err)

	var realAddressPosition = 0
	//initialize first element
	var vt virtual
	vt.real = make(map[int]realServers)
	var currentVirtualServer string

	for i := 0; i < len(data); i++ {
		if vReg.MatchString(data[i]) {
			s := vReg.FindStringSubmatch(data[i])
			realAddressPosition = 0
			currentVirtualServer = s[1]
			z := subVirtualReg.FindStringSubmatch(s[1])
			vt.hostname = z[1]
			p, _ := strconv.Atoi(z[2])
			vt.port = p
		}

		if protocolReg.MatchString(data[i]) {
			s := protocolReg.FindStringSubmatch(data[i])
			vt.protocol = s[1]
		}

		if schedulerReg.MatchString(data[i]) {
			s := schedulerReg.FindStringSubmatch(data[i])
			vt.scheduler = s[1]
		}

		if realReg.MatchString(data[i]) {
			s := realReg.FindStringSubmatch(data[i])
			var srv realServers
			srv.address = s[1]
			if !checkSpecifiedPodExists(cnf.clientSet, srv.address, cnf.service.namespace) {
				continue
			}

			port, _ := strconv.Atoi(s[2])
			srv.port = port
			weight, _ := strconv.Atoi(s[3])
			srv.weight = weight

			vt.real[realAddressPosition] = srv
			realAddressPosition++
		}

		if emptyReg.MatchString(data[i]) {
			globalConfig[currentVirtualServer] = vt
			vt.real = make(map[int]realServers)
		}

	}
}

func updateWight(lvs string, srv realServers) {
	realData := globalConfig[lvs].real
	for i := 0; i < len(realData); i++ {
		if realData[i].address == srv.address && realData[i].port == srv.port {
			globalConfig[lvs].real[i] = srv
			return
		}
	}
	globalConfig[lvs].real[len(realData)] = srv
	return
}

func updateWeighAllLVS(srv realServers) {
	for k := range globalConfig {
		updateWight(k, srv)
	}
}

func writeEach(virtualAddress string, v virtual, f *os.File) {
	_, _ = f.WriteString(fmt.Sprintf("virtual = %s\n", virtualAddress))
	_, _ = f.WriteString(fmt.Sprintf("     protocol = %s\n", v.protocol))
	_, _ = f.WriteString(fmt.Sprintf("     scheduler = %s\n", v.scheduler))
	for i := 0; i < len(v.real); i++ {
		if v.real[i].address != "" {
			_, _ = f.WriteString(fmt.Sprintf("     real = %s:%d gate %d\n", v.real[i].address, v.real[i].port, v.real[i].weight))
		}
	}
	_, _ = f.WriteString("\n")
}

//TODO: make this function stable
//because range is chaos order
func writeConfig(path string) {
	f, err := os.Create(path)
	check(err)
	defer f.Close()

	for virtualHost, v := range globalConfig {
		writeEach(virtualHost, v, f)
	}

	f.Sync()
}

func checkSpecifiedPodExists(set *kubernetes.Clientset, podName string, namespace string) bool {
	_, err := set.CoreV1().Pods(namespace).Get(podName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	return true
}

type statesType map[string]bool

var states = make(statesType)

func addNewConnectorToLVSGracefully(pod v1.Pod, progress int) {
	if states[pod.Name] == true {
		return
	}

	i := progress
	for {
		i += 20
		if i > 100 {
			i = 100
		}
		if states[pod.Name] == false {
			states[pod.Name] = true
		}

		if !checkSpecifiedPodExists(cnf.clientSet, pod.Name, pod.Namespace) {
			states[pod.Name] = false
			return
		}

		progress := strconv.Itoa(i)
		data := []byte(`{"metadata": {"labels": {"progress": "` + progress + `"}}}}`)
		_, err := cnf.clientSet.CoreV1().Pods(cnf.service.namespace).Patch(pod.Name, types.StrategicMergePatchType, data, "")
		check(err)
		var srv realServers
		srv.address = pod.Name
		srv.port = cnf.service.destinationPort
		srv.weight = i
		updateWeighAllLVS(srv)
		writeConfig(cnf.configFile.name)

		if i == 100 {
			delete(states, pod.Name)
			break
		} else {
			time.Sleep(cnf.service.sleepTime * time.Second)
		}
	}

}

func deleteRealServer(lvs string, hostname string) {
	data := globalConfig[lvs].real
	for i := 0; i < len(data); i++ {
		if data[i].address == hostname {
			delete(globalConfig[lvs].real, i)
			writeConfig(cnf.configFile.name)
		}
	}
}

func deleteRealServerAllLVS(hostname string) {
	for k := range globalConfig {
		deleteRealServer(k, hostname)
	}
}

func loadActualStateOfInfrastructure() {
	pods, err := cnf.clientSet.CoreV1().Pods(cnf.service.namespace).List(metav1.ListOptions{LabelSelector: cnf.service.label})
	check(err)
	for _, pod := range pods.Items {
		var srv realServers
		srv.address = pod.Name
		srv.port = cnf.service.destinationPort
		weight, _ := strconv.Atoi(pod.Labels["progress"])
		srv.weight = weight
		updateWeighAllLVS(srv)
	}
	writeConfig(cnf.configFile.name)
}

func detectNewConnectors(c configuration) {
	pods, err := c.clientSet.CoreV1().Pods(c.service.namespace).List(metav1.ListOptions{LabelSelector: c.service.label})
	check(err)
	log.Printf("There are %d pods now in the cluster with this label: %s\n", len(pods.Items), c.service.label)
	if len(pods.Items) == 0 {
		log.Fatalf("No running pods with label: %s", c.service.label)
		log.Fatal("Please ensure what you have at least one pod up and running")
	}

	watcher, err := c.clientSet.CoreV1().Pods(c.service.namespace).Watch(metav1.ListOptions{LabelSelector: c.service.label})
	check(err)

	ch := watcher.ResultChan()

	for event := range ch {
		pod, ok := event.Object.(*v1.Pod)
		if !ok {
			log.Fatal("unexpected type")
		}

		log.Println("Detect new pod: ", pod.Name)
		progress, err := strconv.Atoi(pod.Labels["progress"])
		check(err)

		switch event.Type {

		case watch.Added:
			if pod.Status.Phase == "Running" {
				if progress != 100 {
					go addNewConnectorToLVSGracefully(*pod, progress)
				}
			}
		case watch.Modified:
			if pod.Status.Phase == "Running" {
				if progress != 100 {
					go addNewConnectorToLVSGracefully(*pod, progress)
				}
			}
		case watch.Deleted:
			deleteRealServerAllLVS(pod.Name)
			log.Printf("Connector deleted: %s\n", pod.Name)
			log.Printf("Please avoid this! This is unexpected to you TCP clients")
		}
	}
}

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	initNow()
	readWholeIni(cnf.configFile.name)
	loadActualStateOfInfrastructure()
	go detectNewConnectors(cnf)

	for {
		for _ = range c {
			log.Println("Ctrl+C pressed, exiting...")
			os.Exit(0)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
