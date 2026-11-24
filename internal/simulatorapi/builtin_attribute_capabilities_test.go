package simulatorapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

func TestCapabilitiesHTTPExposesBuiltinAttribute(t *testing.T) {
	runtime, err := simulator.NewSimulator(simulator.Scenario{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var capabilities CapabilitiesResponse
	if err := json.NewDecoder(response.Body).Decode(&capabilities); err != nil {
		t.Fatal(err)
	}
	want := BuiltinAttributeCapability{Name: "createTime", Type: "int64", ReadOnly: true, Source: "ticket.createdAt", IndexTypes: []string{"int64_range"}}
	if len(capabilities.BuiltinAttributes) != 1 || !reflect.DeepEqual(capabilities.BuiltinAttributes[0], want) {
		t.Fatalf("builtin attribute wire capabilities = %#v", capabilities.BuiltinAttributes)
	}
}
