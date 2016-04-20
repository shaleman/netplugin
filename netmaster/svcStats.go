/***
Copyright 2014 Cisco Systems Inc. All rights reserved.

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

package main

import (
	"fmt"
	"net/http"
	"time"
  "errors"
  "encoding/json"
  "io/ioutil"

	"github.com/contiv/netplugin/netmaster/mastercfg"
	"github.com/contiv/netplugin/utils"
	"github.com/contiv/ofnet"

	log "github.com/Sirupsen/logrus"
)

type PktStats struct {
	PacketsOut uint64
	PacketsIn  uint64
	BytesOut uint64
	BytesIn uint64
}

type PktStatsHistory struct {
	maxCount int
	currCount int
	History []*PktStats
}

type InstPairStats struct {
	FromService string
	FromAddr string
	ToService string
	ToAddr string
	PktStats
}

type ServicePairStats struct {
	FromService string
	ToService string
	PktStats
}

type InstStats struct {
	ServiceName string
	Addr string
	PktStats
}

type ServiceStats struct {
	ServiceName string
	PktStats
}

type SvcStats struct {
	Services  map[string]*ServiceStats
	Instances map[string]*InstStats
	ServicePairs map[string]*ServicePairStats
	InstPairs map[string]*InstPairStats
	ServicePairHistory map[string]*PktStatsHistory
}

// Max number of history elements
var MaxPktHistory = 100

var svcStats SvcStats

func httpGet(url string, jdata interface{}) error {

	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	switch {
	case r.StatusCode == int(404):
		return errors.New("Page not found!")
	case r.StatusCode == int(403):
		return errors.New("Access denied!")
	case r.StatusCode == int(500):
		response, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return err
		}

		return errors.New(string(response))

	case r.StatusCode != int(200):
		log.Debugf("GET Status '%s' status code %d \n", r.Status, r.StatusCode)
		return errors.New(r.Status)
	}

	response, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(response, jdata); err != nil {
		return err
	}

	return nil
}

// getSvcStats get service statistics
func (d *daemon) getSvcStats() []byte {
  jsonStats, err := json.Marshal(d.svcStats)
  if err != nil {
    log.Errorf("Error encoding svc stats. Err: %v", err)
  }

  return jsonStats
}

// getSvcStats get service statistics
func (d *daemon) getServiceStats() []byte {
  jsonStats, err := json.Marshal(svcStats)
  if err != nil {
    log.Errorf("Error encoding service stats. Err: %v", err)
  }

  return jsonStats
}

func (d *daemon) pollSvcStats() {
	var err error

	// init svc stats
	svcStats.Services = make(map[string]*ServiceStats)
	svcStats.Instances = make(map[string]*InstStats)
	svcStats.ServicePairs = make(map[string]*ServicePairStats)
	svcStats.InstPairs = make(map[string]*InstPairStats)
	svcStats.ServicePairHistory = make(map[string]*PktStatsHistory)

	// Setup state driver to read all endpoints
	epState := &mastercfg.CfgEndpointState{}
	if epState.StateDriver, err = utils.GetStateDriver(); err != nil {
		return
	}

  // Dont start polling immediately
  time.Sleep(5 * time.Second)

  // poll forever
  for {
    time.Sleep(2 * time.Second)

    // Get all netplugin services
    srvList, err := d.objdbClient.GetService("netplugin")
    if err != nil {
      log.Errorf("Error getting netplugin nodes. Err: %v", err)
      continue
    }

		var ofStats = make(map[string]*ofnet.OfnetEPStats)

    // Add each node
    for _, srv := range srvList {
      var statsList []*ofnet.OfnetEPStats
      err := httpGet(fmt.Sprintf("http://%s:9090/svcstats", srv.HostAddr), &statsList)
    	if err != nil {
    		log.Errorf("Error getting appProfiles. Err: %v", err)
    	} else {
        // parse each stats entry
        for _, epStats := range statsList {
          d.svcStats[epStats.EndpointIP] = epStats
					ofStats[epStats.EndpointIP] = epStats
          log.Infof("Saving stats info: %+v", epStats)
        }
      }
    }

		// Get endpoints info
		eps, err := epState.ReadAll()
		if err != nil {
			log.Errorf("Error reading endpoints. Err: %v", err)
			continue
		}

		// build epDb for lookup based on ip addr
		var epDb = make(map[string]*mastercfg.CfgEndpointState)
		for _, tmpep := range eps {
			ep := tmpep.(*mastercfg.CfgEndpointState)
			epDb[ep.IPAddress] = ep
		}

		// clear svc stats
		svcStats.Services = make(map[string]*ServiceStats)
		svcStats.Instances = make(map[string]*InstStats)
		svcStats.ServicePairs = make(map[string]*ServicePairStats)
		svcStats.InstPairs = make(map[string]*InstPairStats)

		// process stats
		for epAddr, epStat := range ofStats {
			// loop thru each service
			for _, svStat := range epStat.SvcStats {
				// Lookup consumer and provider endpoints
				conEp := epDb[epAddr]
				provEp := epDb[svStat.ProviderIP]
				if conEp == nil || provEp == nil {
					continue
				}

				// Lookup service names
				conService := conEp.Labels["app"]
				provService := provEp.Labels["app"]
				if conService == "" || provService == "" {
					continue
				}

				// Accumulate state
				setSrvStats(conService, epAddr, provService, svStat.ProviderIP, svStat.Stats)
			}
		}
  }
}

// setSrvStats process and save stats
func setSrvStats(conServ, conIP, provServ, provIP string, stats ofnet.OfnetDPStats) {
	if svcStats.Services[conServ] == nil {
		svcStats.Services[conServ] = new(ServiceStats)
	}
	conSrv := svcStats.Services[conServ]
	if svcStats.Services[provServ] == nil {
		svcStats.Services[provServ] = new(ServiceStats)
	}
	provSrv := svcStats.Services[provServ]

	// Set service stats
	conSrv.ServiceName = conServ
	conSrv.PacketsOut += stats.PacketsOut
	conSrv.BytesOut += stats.BytesOut
	conSrv.PacketsIn += stats.PacketsIn
	conSrv.BytesIn += stats.BytesIn
	provSrv.ServiceName = provServ
	provSrv.PacketsIn += stats.PacketsOut
	provSrv.BytesIn += stats.BytesOut
	provSrv.PacketsOut += stats.PacketsIn
	provSrv.BytesOut += stats.BytesIn

	// Service pair stats
	srvPairKey := conServ + ":" + provServ
	if svcStats.ServicePairs[srvPairKey] == nil {
		svcStats.ServicePairs[srvPairKey] = new(ServicePairStats)
	}
	srvPair := svcStats.ServicePairs[srvPairKey]

	srvPair.FromService = conServ
	srvPair.ToService = provServ
	srvPair.PacketsOut += stats.PacketsOut
	srvPair.BytesOut += stats.BytesOut
	srvPair.PacketsIn += stats.PacketsIn
	srvPair.BytesIn += stats.BytesIn

	// Add service pair history element
	addServicePairHistory(srvPairKey, &srvPair.PktStats)

	// Service instance stats
	conServInstKey := conServ + ":" + conIP
	provServInstKey := provServ + ":" + provIP
	if svcStats.Instances[conServInstKey] == nil {
		svcStats.Instances[conServInstKey] = new(InstStats)
	}
	if svcStats.Instances[provServInstKey] == nil {
		svcStats.Instances[provServInstKey] = new(InstStats)
	}

	conServInst := svcStats.Instances[conServInstKey]
	provServInst := svcStats.Instances[provServInstKey]

	conServInst.ServiceName = conServ
	conServInst.Addr = conIP
	conServInst.PacketsOut += stats.PacketsOut
	conServInst.BytesOut += stats.BytesOut
	conServInst.PacketsIn += stats.PacketsIn
	conServInst.BytesIn += stats.BytesIn

	provServInst.ServiceName = provServ
	provServInst.Addr = provIP
	provServInst.PacketsIn += stats.PacketsOut
	provServInst.BytesIn += stats.BytesOut
	provServInst.PacketsOut += stats.PacketsIn
	provServInst.BytesOut += stats.BytesIn

	// Service instance pair stats
	servInstPairKey := conServInstKey + ":" + provServInstKey
	if svcStats.InstPairs[servInstPairKey] == nil {
		svcStats.InstPairs[servInstPairKey] = new(InstPairStats)
	}
	servInstPair := svcStats.InstPairs[servInstPairKey]
	servInstPair.FromService = conServ
	servInstPair.ToService = provServ
	servInstPair.FromAddr = conIP
	servInstPair.ToAddr = provIP
	servInstPair.PacketsOut += stats.PacketsOut
	servInstPair.BytesOut += stats.BytesOut
	servInstPair.PacketsIn += stats.PacketsIn
	servInstPair.BytesIn += stats.BytesIn
}

func newPktStatsHistory(maxCount int) *PktStatsHistory {
	return &PktStatsHistory{
		maxCount: maxCount,
	}
}

func addPktStatsHistory(hist *PktStatsHistory, stats *PktStats) {
	hist.History = append(hist.History, stats)
	if hist.currCount >= hist.maxCount {
		hist.History = hist.History[1:]
	} else {
		hist.currCount++
	}
}


func addServicePairHistory(svcPairKey string, stats *PktStats) {
	if svcStats.ServicePairHistory[svcPairKey] == nil {
		svcStats.ServicePairHistory[svcPairKey] = newPktStatsHistory(MaxPktHistory)
	}

	// Add it to history
	addPktStatsHistory(svcStats.ServicePairHistory[svcPairKey], stats)
}
