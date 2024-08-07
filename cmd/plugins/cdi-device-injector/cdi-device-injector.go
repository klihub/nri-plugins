// Copyright The NRI Plugins Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"
	"tags.cncf.io/container-device-interface/pkg/cdi"
	"tags.cncf.io/container-device-interface/pkg/parser"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"

	"github.com/containers/nri-plugins/pkg/kubernetes/client"
	"github.com/containers/nri-plugins/pkg/kubernetes/watch"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	cdiDeviceKey    = "cdi.nri.io"
	allowPatternKey = cdiDeviceKey + "/" + "allow"
	nsEnvVar        = "POD_NAMESPACE"
)

var (
	log     *logrus.Logger
	verbose bool
)

// our injector plugin
type plugin struct {
	stub                    stub.Stub
	defaultCDIDevicePattern string
	allowedCDIDevicePattern string
	allowedLock             sync.Mutex
	cdiCache                *cdiCache

	kubeConfig string
	namespace  string
	client     *client.Client
	w          watch.Interface
}

func (p *plugin) onClose() {
	log.Error("connection to NRI/runtime lost, exiting...")
	os.Exit(1)
}

// CreateContainer handles container creation requests.
func (p *plugin) CreateContainer(ctx context.Context, pod *api.PodSandbox, container *api.Container) (_ *api.ContainerAdjustment, _ []*api.ContainerUpdate, err error) {
	defer func() {
		if err != nil {
			log.Error(err)
		}
	}()
	name := containerName(pod, container)

	if verbose {
		dump("CreateContainer", "pod", pod, "container", container)
	} else {
		log.Infof("CreateContainer %s", name)
	}

	if p.allowedCDIDevicePattern == "" {
		return nil, nil, nil
	}

	cdiDevices, err := parseCdiDevices(pod.Annotations, container.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CDI Device annotations: %w", err)
	}

	if len(cdiDevices) == 0 {
		return nil, nil, nil
	}

	var allowedCDIDevices []string
	for _, cdiDevice := range cdiDevices {
		match, _ := filepath.Match(p.allowedCDIDevicePattern, cdiDevice)
		if !match {
			continue
		}
		allowedCDIDevices = append(allowedCDIDevices, cdiDevice)
	}

	adjust := &api.ContainerAdjustment{}
	if _, err := p.cdiCache.InjectDevices(adjust, allowedCDIDevices...); err != nil {
		return nil, nil, fmt.Errorf("CDI device injection failed: %w", err)
	}

	return adjust, nil, nil
}

func (p *plugin) setupNamespaceWatch() error {
	p.namespace = os.Getenv(nsEnvVar)
	if p.namespace == "" {
		log.Warnf("%q not set in environment, will not watch namespace", nsEnvVar)
		return nil
	}

	log.Infof("will watch namespace %q", p.namespace)

	cli, err := client.New(client.WithKubeOrInClusterConfig(p.kubeConfig))
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	p.client = cli

	w, err := watch.Object(p, "allowed-device-pattern")
	if err != nil {
		return err
	}
	p.w = w

	go func() {
		for e := range p.w.ResultChan() {
			switch e.Type {
			case watch.Added, watch.Modified:
				if ns, ok := e.Object.(*corev1.Namespace); ok {
					if pattern, ok := ns.Annotations[allowPatternKey]; ok {
						p.setAllowedPattern(pattern)
					}
				}
			}
		}
	}()

	return nil
}

func (p plugin) CreateWatch() (watch.Interface, error) {
	return p.client.CoreV1().Namespaces().Watch(
		context.Background(),
		metav1.ListOptions{
			FieldSelector: "metadata.name=" + p.namespace,
		},
	)
}

func (p *plugin) setAllowedPattern(pattern string) {
	p.allowedLock.Lock()
	defer p.allowedLock.Unlock()

	if pattern == p.allowedCDIDevicePattern {
		return
	}

	p.allowedCDIDevicePattern = pattern
	log.Infof("allowed CDI device pattern is now %q", pattern)
}

func parseCdiDevices(annotations map[string]string, ctr string) ([]string, error) {
	var errs error
	var cdiDevices []string

	for _, key := range []string{
		cdiDeviceKey + "/container." + ctr,
		cdiDeviceKey + "/pod",
		cdiDeviceKey,
	} {
		if value, ok := annotations[key]; ok {
			for _, device := range strings.Split(value, ",") {
				if !parser.IsQualifiedName(device) {
					errs = errors.Join(errs, fmt.Errorf("invalid CDI device name %v", device))
					continue
				}
				cdiDevices = append(cdiDevices, device)
			}
		}
	}
	return cdiDevices, errs
}

// Construct a container name for log messages.
func containerName(pod *api.PodSandbox, container *api.Container) string {
	if pod != nil {
		return pod.Namespace + "/" + pod.Name + "/" + container.Name
	}
	return container.Name
}

// Dump one or more objects, with an optional global prefix and per-object tags.
func dump(args ...interface{}) {
	var (
		prefix string
		idx    int
	)

	if len(args)&0x1 == 1 {
		prefix = args[0].(string)
		idx++
	}

	for ; idx < len(args)-1; idx += 2 {
		tag, obj := args[idx], args[idx+1]
		msg, err := yaml.Marshal(obj)
		if err != nil {
			log.Infof("%s: %s: failed to dump object: %v", prefix, tag, err)
			continue
		}

		if prefix != "" {
			log.Infof("%s: %s:", prefix, tag)
			for _, line := range strings.Split(strings.TrimSpace(string(msg)), "\n") {
				log.Infof("%s:    %s", prefix, line)
			}
		} else {
			log.Infof("%s:", tag)
			for _, line := range strings.Split(strings.TrimSpace(string(msg)), "\n") {
				log.Infof("  %s", line)
			}
		}
	}
}

func main() {
	var (
		pluginName              string
		pluginIdx               string
		defaultCDIDevicePattern string
		kubeConfig              string
		err                     error
	)

	log = logrus.StandardLogger()
	log.SetFormatter(&logrus.TextFormatter{
		PadLevelText: true,
	})

	flag.StringVar(&pluginName, "name", "", "plugin name to register to NRI")
	flag.StringVar(&pluginIdx, "idx", "", "plugin index to register to NRI")
	flag.StringVar(&defaultCDIDevicePattern, "default-cdi-device-pattern", "*", "default glob pattern for allowed CDI device names if namespace is not annotated with "+allowPatternKey)
	flag.StringVar(&kubeConfig, "kubeconfig", "", "kubeconfig file to use")
	flag.BoolVar(&verbose, "verbose", false, "enable (more) verbose logging")
	flag.Parse()

	p := &plugin{
		defaultCDIDevicePattern: defaultCDIDevicePattern,
		cdiCache: &cdiCache{
			// TODO: We should allow this to be configured
			Cache: cdi.GetDefaultCache(),
		},
		kubeConfig: kubeConfig,
	}

	opts := []stub.Option{stub.WithOnClose(p.onClose)}

	if pluginName != "" {
		opts = append(opts, stub.WithPluginName(pluginName))
	}
	if pluginIdx != "" {
		opts = append(opts, stub.WithPluginIdx(pluginIdx))
	}

	p.setAllowedPattern(defaultCDIDevicePattern)

	if err := p.setupNamespaceWatch(); err != nil {
		log.Fatalf("failed to set up namespace watch: %v", err)
	}

	if p.stub, err = stub.New(p, opts...); err != nil {
		log.Fatalf("failed to create plugin stub: %v", err)
	}

	err = p.stub.Run(context.Background())
	if err != nil {
		log.Fatalf("plugin exited with error %v", err)
	}
}
