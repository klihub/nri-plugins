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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"
	client "github.com/containers/nri-plugins/pkg/generated/clientset/versioned"

	"github.com/containers/nri-plugins/pkg/agent/watch"
)

type configKind int

const (
	balloonsConfig configKind = iota
	topologyAwareConfig
	templateConfig
)

type configIf struct {
	kind configKind
	cfg  *rest.Config
	cli  *client.Clientset
}

func newConfigIf(kind configKind) *configIf {
	return &configIf{
		kind: kind,
	}
}

func (cif *configIf) SetKubeClient(httpCli *http.Client, restCfg *rest.Config) error {
	cfg := *restCfg
	cli, err := client.NewForConfigAndClient(&cfg, httpCli)
	if err != nil {
		return fmt.Errorf("failed to create client for config resource access: %w", err)
	}

	cif.cfg = &cfg
	cif.cli = cli
	return nil
}

func (cif *configIf) CreateWatch(ctx context.Context, ns, name string) (watch.Interface, error) {
	var timeout *int64

	if watchTestTimeout != 0 {
		timeout = &watchTestTimeout
	}

	selector := metav1.ListOptions{
		FieldSelector:  "metadata.name=" + name,
		TimeoutSeconds: timeout,
	}

	switch cif.kind {
	case balloonsConfig:
		return cif.cli.ConfigV1alpha1().BalloonsConfigs(ns).Watch(ctx, selector)
	case topologyAwareConfig:
		return cif.cli.ConfigV1alpha1().TopologyAwareConfigs(ns).Watch(ctx, selector)
	case templateConfig:
		return cif.cli.ConfigV1alpha1().TemplateConfigs(ns).Watch(ctx, selector)
	}
	return nil, fmt.Errorf("configIf: unknown config type %v", cif.kind)
}

type mergePatchConfig struct {
	Status mergePatchStatus `json:"status,omitempty"`
}

type mergePatchStatus struct {
	Nodes map[string]*cfgapi.NodeStatus `json:"nodes,omitempty"`
}

func (cif *configIf) PatchStatus(ctx context.Context, ns, name, node string, s *cfgapi.NodeStatus) error {
	if cif.cli == nil {
		return nil
	}

	var (
		pType = k8stypes.MergePatchType
		pOpts = metav1.PatchOptions{}
	)

	switch cif.kind {
	case balloonsConfig:
		cfg := &mergePatchConfig{
			Status: mergePatchStatus{
				Nodes: map[string]*cfgapi.NodeStatus{
					node: s,
				},
			},
		}
		pData, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal JSON patch: %v", err)
		}

		_, err = cif.cli.ConfigV1alpha1().BalloonsConfigs(ns).Patch(ctx, name, pType, pData, pOpts, "status")
		if err != nil {
			return fmt.Errorf("patching status failed: %v", err)
		}

	case topologyAwareConfig:
		cfg := &mergePatchConfig{
			Status: mergePatchStatus{
				Nodes: map[string]*cfgapi.NodeStatus{
					node: s,
				},
			},
		}
		pData, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal JSON patch: %v", err)
		}
		_, err = cif.cli.ConfigV1alpha1().TopologyAwareConfigs(ns).Patch(ctx, name, pType, pData, pOpts, "status")
		if err != nil {
			return fmt.Errorf("patching status failed: %v", err)
		}

	case templateConfig:

		cfg := &cfgapi.TemplateConfig{
			Status: cfgapi.ConfigStatus{
				Nodes: map[string]cfgapi.NodeStatus{
					node: *s,
				},
			},
		}
		pData, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal JSON patch: %v", err)
		}
		_, err = cif.cli.ConfigV1alpha1().TemplateConfigs(ns).Patch(ctx, name, pType, pData, pOpts, "status")
		if err != nil {
			return fmt.Errorf("patching status failed: %v", err)
		}
	}

	return nil
}

func (cif *configIf) WatchFile(file string) (watch.Interface, error) {
	var unmarshal func([]byte) (runtime.Object, error)

	switch cif.kind {
	case balloonsConfig:
		unmarshal = func(data []byte) (runtime.Object, error) {
			cfg := &cfgapi.BalloonsConfig{}
			err := yaml.UnmarshalStrict(data, cfg)
			if err != nil {
				return nil, err
			}
			cfg.Name = file
			return cfg, nil
		}
	case topologyAwareConfig:
		unmarshal = func(data []byte) (runtime.Object, error) {
			cfg := &cfgapi.TopologyAwareConfig{}
			err := yaml.UnmarshalStrict(data, cfg)
			if err != nil {
				return nil, err
			}
			cfg.Name = file
			return cfg, nil
		}
	case templateConfig:
		unmarshal = func(data []byte) (runtime.Object, error) {
			cfg := &cfgapi.TemplateConfig{}
			err := yaml.UnmarshalStrict(data, cfg)
			if err != nil {
				return nil, err
			}
			cfg.Name = file
			return cfg, nil
		}
	}

	return watch.File(file, unmarshal)
}

var (
	watchTestTimeout int64
)

func init() {
	const envVar = "AGENT_FORCE_WATCH_TIMEOUT"

	if val := os.Getenv(envVar); val != "" {
		timeout, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			log.Fatal("failed to parse %s %s: %v", envVar, val, err)
		}
		watchTestTimeout = timeout
	}
}
