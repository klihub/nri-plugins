package main

import (
	"flag"
	"fmt"

	"sigs.k8s.io/yaml"

	"github.com/containers/nri-plugins/pkg/agent"
	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"
	logger "github.com/containers/nri-plugins/pkg/log"
)

var (
	log        = logger.Get("agent-test")
	configType string
)

func configUpdate(newCfg interface{}) error {
	if newCfg == nil {
		return fmt.Errorf("no effective configuration")
	}

	cfg, ok := newCfg.(cfgapi.ResmgrConfig)
	if !ok {
		return fmt.Errorf("configuration of wrong type %T", newCfg)
	}

	meta := cfg.(cfgapi.AnyConfig).GetObjectMeta()

	log.Infof("configuration updated: %s (%s), generation %d, version %s",
		meta.Name, meta.UID, meta.Generation, meta.ResourceVersion)

	dump, _ := yaml.Marshal(cfg)
	log.InfoBlock("  <"+configType+"> ", "%s", dump)

	return nil
}

func main() {
	var (
		cfgIf    agent.ConfigInterface
		options  []agent.Option
		updateFn agent.NotifyFn
	)

	flag.StringVar(&configType, "config-type", "topology-aware",
		"configuration type, one of {topology-aware, balloons}")
	flag.Parse()

	switch configType {
	case "balloons":
		cfgIf = agent.BalloonsConfigInterface()
		updateFn = configUpdate
	case "topology-aware":
		cfgIf = agent.TopologyAwareConfigInterface()
		updateFn = configUpdate
	case "template":
		cfgIf = agent.TemplateConfigInterface()
		updateFn = configUpdate
	default:
		log.Fatalf("invalid config type %s", configType)
	}

	a, err := agent.New(cfgIf, options...)
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
	}

	err = a.Start(updateFn)
	if err != nil {
		log.Fatalf("failed to start agent: %v", err)
	}
}
