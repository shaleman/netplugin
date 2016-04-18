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

	"github.com/contiv/ofnet"

	log "github.com/Sirupsen/logrus"
)

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

func (d *daemon) pollSvcStats() {
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
          log.Infof("Saving stats info: %+v", epStats)
        }
      }
    }
  }
}
