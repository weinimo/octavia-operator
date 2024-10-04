/*
Copyright 2024.

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

package functional_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-logr/logr"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/quotasets"

	api "github.com/openstack-k8s-operators/lib-common/modules/test/apis"
)

type NovaAPIFixture struct {
	api.APIFixture
	QuotaSets       map[string]quotasets.QuotaSet
	DefaultQuotaSet quotasets.QuotaSet
}

func (f *NovaAPIFixture) registerHandler(handler api.Handler) {
	f.Server.AddHandler(f.URLBase+handler.Pattern, handler.Func)
}

func (f *NovaAPIFixture) Setup() {
	f.registerHandler(api.Handler{Pattern: "/os-quota-sets/", Func: f.quotaSetsHandler})
}

func (f *NovaAPIFixture) quotaSetsHandler(w http.ResponseWriter, r *http.Request) {
	f.LogRequest(r)
	switch r.Method {
	case "GET":
		f.getQuotaSets(w, r)
	case "PUT":
		f.putQuotaSets(w, r)
	default:
		f.UnexpectedRequest(w, r)
		return
	}
}

func (f *NovaAPIFixture) getQuotaSets(w http.ResponseWriter, r *http.Request) {
	items := strings.Split(r.URL.Path, "/")
	tenantID := items[len(items)-1]

	var q struct {
		Quotaset quotasets.QuotaSet `json:"quota_set"`
	}
	if quotaset, ok := f.QuotaSets[tenantID]; ok {
		q.Quotaset = quotaset
	} else {
		q.Quotaset = f.DefaultQuotaSet
	}
	bytes, err := json.Marshal(&q)
	if err != nil {
		f.InternalError(err, "Error during marshalling response", w, r)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	fmt.Fprint(w, string(bytes))
}

func (f *NovaAPIFixture) putQuotaSets(w http.ResponseWriter, r *http.Request) {
	items := strings.Split(r.URL.Path, "/")
	tenantID := items[len(items)-1]

	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		f.InternalError(err, "Error reading request body", w, r)
		return
	}
	var q struct {
		Quotaset quotasets.QuotaSet `json:"quota_set"`
	}
	err = json.Unmarshal(bytes, &q)
	if err != nil {
		f.InternalError(err, "Error during unmarshalling request", w, r)
		return
	}
	f.QuotaSets[tenantID] = q.Quotaset

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	fmt.Fprint(w, string(bytes))
}

func NewNovaAPIFixtureWithServer(log logr.Logger) *NovaAPIFixture {
	server := &api.FakeAPIServer{}
	server.Setup(log)
	fixture := AddNovaAPIFixture(log, server)
	fixture.OwnsServer = true
	return fixture
}

func AddNovaAPIFixture(log logr.Logger, server *api.FakeAPIServer) *NovaAPIFixture {
	fixture := &NovaAPIFixture{
		APIFixture: api.APIFixture{
			Server:     server,
			Log:        log,
			URLBase:    "/compute",
			OwnsServer: false,
		},
		DefaultQuotaSet: quotasets.QuotaSet{
			RAM:                100,
			Cores:              100,
			Instances:          50,
			ServerGroups:       10,
			ServerGroupMembers: 10,
		},
		QuotaSets: map[string]quotasets.QuotaSet{},
	}
	return fixture
}