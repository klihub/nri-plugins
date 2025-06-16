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

package resmgr

import (
	"fmt"

	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/control/blockio"
	logger "github.com/containers/nri-plugins/pkg/log"
)

type blkioControl struct {
	resmgr   *resmgr
	hostRoot string
}

func newBlockioControl(resmgr *resmgr, hostRoot string) *blkioControl {
	blockio.SetLogger(logger.Get("goresctrl"))

	if hostRoot != "" {
		blockio.SetPrefix(opt.HostRoot)
	}

	return &blkioControl{
		resmgr:   resmgr,
		hostRoot: hostRoot,
	}
}

func (c *blkioControl) configure(cfg *blockio.Config) error {
	if cfg == nil {
		return nil
	}

	if cfg.Enable {
		nativeCfg, force := cfg.ToGoresctrl()

		if nativeCfg != nil {
			if err := blockio.SetConfig(nativeCfg, force); err != nil {
				return fmt.Errorf("failed to configure goresctrl/blockio: %w", err)
			}
			log.Info("goresctrl/blockio configuration updated")
		}
	}

	c.resmgr.cache.ConfigureBlockIOControl(cfg.Enable)

	return nil
}
