package httpapi_test

import (
	"context"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/httpapi"
)

func TestUploadProbeResultsRequiresAgentPrincipal(t *testing.T) {
	server := httpapi.NewServer(httpapi.ServerConfig{})
	response, err := server.UploadProbeResults(
		context.Background(),
		httpapi.UploadProbeResultsRequestObject{
			Body: &httpapi.UploadProbeResultsJSONRequestBody{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.UploadProbeResultsdefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 401 || problem.Body.Code != "unauthorized" {
		t.Fatalf("response = %#v", response)
	}
}
